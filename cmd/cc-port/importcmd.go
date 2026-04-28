package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/file"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/scan"
	"github.com/it-bens/cc-port/internal/ui"
)

var (
	importFromManifest           string
	importResolutionKV           []string
	importManifestOutput         string
	importPassphraseEnv          string
	importPassphraseFile         string
	importManifestPassphraseEnv  string
	importManifestPassphraseFile string
)

var importCmd = &cobra.Command{
	Use:   "import <archive.zip> <target-path>",
	Short: "Import a project from a cc-port ZIP archive",
	Long:  "Imports Claude Code project data from a ZIP archive into the given target path.",
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(2)(cmd, args); err != nil {
			return &usageError{err: err}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) (err error) {
		archivePath := args[0]
		targetPath, err := claude.ResolveProjectPath(args[1])
		if err != nil {
			return fmt.Errorf("resolve target path: %w", err)
		}

		claudeHome, err := claude.NewHome(claudeDir)
		if err != nil {
			return err
		}

		flagResolutions, err := parseResolutionFlags(importResolutionKV)
		if err != nil {
			return err
		}

		passphrase, err := resolvePassphrase(importPassphraseEnv, importPassphraseFile)
		if err != nil {
			return err
		}

		source, err := pipeline.RunReader(cmd.Context(), []pipeline.ReaderStage{
			&file.Source{Path: archivePath},
			&encrypt.ReaderStage{Pass: passphrase, Mode: encrypt.Strict},
		})
		if err != nil {
			return fmt.Errorf("open archive source: %w", err)
		}
		defer func() {
			if cerr := source.Close(); cerr != nil {
				err = errors.Join(err, fmt.Errorf("close archive source: %w", cerr))
			}
		}()

		var resolutions map[string]string
		if importFromManifest != "" {
			metadata, err := manifest.ReadManifest(importFromManifest)
			if err != nil {
				return fmt.Errorf("read manifest: %w", err)
			}
			resolutions = resolutionsFromManifest(metadata, targetPath)
			for key, value := range flagResolutions {
				resolutions[key] = value
			}
		} else {
			resolutions, err = promptImportResolutions(source.ReaderAt, source.Size, targetPath, flagResolutions)
			if err != nil {
				return err
			}
		}

		importOptions := importer.Options{
			Source:      source.ReaderAt,
			Size:        source.Size,
			TargetPath:  targetPath,
			Resolutions: resolutions,
		}

		if err := importer.Run(cmd.Context(), claudeHome, importOptions); err != nil {
			return fmt.Errorf("import: %w", err)
		}

		fmt.Printf("Imported to %s\n", targetPath)

		printImportRulesWarnings(claudeHome, targetPath)

		return nil
	},
}

var importManifestCmd = &cobra.Command{
	Use:   "manifest <archive.zip>",
	Short: "Write a manifest XML from a ZIP archive for manual editing",
	Long:  "Reads the metadata from a ZIP archive and writes a manifest XML with empty resolve fields for hand-editing.",
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return &usageError{err: err}
		}
		return nil
	},
	RunE: runImportManifest,
}

// runImportManifest is the import manifest subcommand body, extracted so
// tests can drive it without re-wiring the whole cobra tree. Refuses to
// overwrite an existing output file; the user deletes it or picks a
// different path with --output.
func runImportManifest(cmd *cobra.Command, args []string) (err error) {
	archivePath := args[0]

	if _, err := os.Stat(importManifestOutput); err == nil {
		return fmt.Errorf("%s already exists; remove it or pass --output", importManifestOutput)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", importManifestOutput, err)
	}

	passphrase, err := resolvePassphrase(importManifestPassphraseEnv, importManifestPassphraseFile)
	if err != nil {
		return err
	}

	source, err := pipeline.RunReader(cmd.Context(), []pipeline.ReaderStage{
		&file.Source{Path: archivePath},
		&encrypt.ReaderStage{Pass: passphrase, Mode: encrypt.Strict},
	})
	if err != nil {
		return fmt.Errorf("open archive source: %w", err)
	}
	defer func() {
		if cerr := source.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close archive source: %w", cerr))
		}
	}()

	metadata, err := manifest.ReadManifestFromZip(source.ReaderAt, source.Size)
	if err != nil {
		return fmt.Errorf("read manifest from zip: %w", err)
	}

	for i := range metadata.Placeholders {
		metadata.Placeholders[i].Resolve = ""
	}

	if err := manifest.WriteManifest(importManifestOutput, metadata); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	fmt.Printf("Manifest written to %s\n", importManifestOutput)
	fmt.Println("Edit the resolve attributes and use --from-manifest to import.")
	return nil
}

func init() {
	importCmd.Flags().StringVar(
		&importFromManifest, "from-manifest", "",
		"path to a manifest XML file with pre-filled resolutions",
	)
	importCmd.Flags().StringArrayVar(
		&importResolutionKV, "resolution", nil,
		"resolve a placeholder non-interactively (repeatable; KEY=VALUE, "+
			"e.g. --resolution '{{HOME}}=/Users/me'). When combined with "+
			"--from-manifest, flag values win per key.",
	)
	importCmd.Flags().StringVar(
		&importPassphraseEnv, "passphrase-env", "",
		"name of the environment variable holding the passphrase "+
			"(mutually exclusive with --passphrase-file)",
	)
	importCmd.Flags().StringVar(
		&importPassphraseFile, "passphrase-file", "",
		"path to a file holding the passphrase, trailing newlines trimmed "+
			"(mutually exclusive with --passphrase-env)",
	)
	importCmd.MarkFlagsMutuallyExclusive("passphrase-env", "passphrase-file")
	importManifestCmd.Flags().StringVarP(
		&importManifestOutput, "output", "o", "manifest.xml",
		"path to write the manifest XML",
	)
	importManifestCmd.Flags().StringVar(
		&importManifestPassphraseEnv, "passphrase-env", "",
		"name of the environment variable holding the passphrase "+
			"(mutually exclusive with --passphrase-file)",
	)
	importManifestCmd.Flags().StringVar(
		&importManifestPassphraseFile, "passphrase-file", "",
		"path to a file holding the passphrase, trailing newlines trimmed "+
			"(mutually exclusive with --passphrase-env)",
	)
	importManifestCmd.MarkFlagsMutuallyExclusive("passphrase-env", "passphrase-file")
	importCmd.AddCommand(importManifestCmd)
	rootCmd.AddCommand(importCmd)
}

func promptImportResolutions(
	archiveSource io.ReaderAt,
	archiveSize int64,
	targetPath string,
	preResolved map[string]string,
) (map[string]string, error) {
	metadata, err := manifest.ReadManifestFromZip(archiveSource, archiveSize)
	if err != nil {
		return nil, fmt.Errorf("read manifest from zip: %w", err)
	}

	resolutions := make(map[string]string, len(metadata.Placeholders))

	for _, placeholder := range metadata.Placeholders {
		if placeholder.Key == importer.ProjectPathKey {
			resolutions[placeholder.Key] = targetPath
			continue
		}

		if value, ok := preResolved[placeholder.Key]; ok {
			resolutions[placeholder.Key] = value
			continue
		}

		if placeholder.Resolve != "" {
			resolutions[placeholder.Key] = placeholder.Resolve
			continue
		}

		resolved, err := ui.ResolvePlaceholder(placeholder.Key, placeholder.Original, "")
		if err != nil {
			return nil, err
		}
		resolutions[placeholder.Key] = resolved
	}

	return resolutions, nil
}

// resolutionsFromManifest copies pre-filled placeholder Resolve values
// into the resolutions map, then forces {{PROJECT_PATH}} to targetPath.
// A sender-supplied {{PROJECT_PATH}} is dropped because importer.Run
// injects it from the import target; a sender resolve would point at
// the sender's disk and silently misroute references in the pulled
// bodies. Same refusal as parseResolutionFlags. Empty Resolve values
// are skipped so importer.ValidateResolutions does not see phantom
// entries for keys the operator never resolved.
func resolutionsFromManifest(metadata *manifest.Metadata, targetPath string) map[string]string {
	resolutions := make(map[string]string, len(metadata.Placeholders))
	for _, placeholder := range metadata.Placeholders {
		if placeholder.Key == importer.ProjectPathKey {
			continue
		}
		if placeholder.Resolve == "" {
			continue
		}
		resolutions[placeholder.Key] = placeholder.Resolve
	}
	resolutions[importer.ProjectPathKey] = targetPath
	return resolutions
}

// parseResolutionFlags parses --resolution flag values into a map. {{PROJECT_PATH}} is
// refused because importer.Run injects it unconditionally from the target
// argument; a flag override would desync the two.
func parseResolutionFlags(raw []string) (map[string]string, error) {
	parsed := make(map[string]string, len(raw))
	for _, entry := range raw {
		equalsIndex := strings.IndexByte(entry, '=')
		if equalsIndex < 0 {
			return nil, fmt.Errorf(
				"--resolution %q: expected KEY=VALUE (no '=' found), "+
					"or use `cc-port import manifest <archive>` to generate a manifest for hand-editing",
				entry,
			)
		}
		key := entry[:equalsIndex]
		value := entry[equalsIndex+1:]
		if key == "" {
			return nil, fmt.Errorf(
				"--resolution %q: empty key, "+
					"or use `cc-port import manifest <archive>` to generate a manifest for hand-editing",
				entry,
			)
		}
		if key == importer.ProjectPathKey {
			return nil, fmt.Errorf(
				"--resolution %s is not allowed: "+
					"the import target argument supplies this key",
				importer.ProjectPathKey,
			)
		}
		if _, duplicate := parsed[key]; duplicate {
			return nil, fmt.Errorf("--resolution %q: duplicate key", key)
		}
		parsed[key] = value
	}
	return parsed, nil
}

func printImportRulesWarnings(claudeHome *claude.Home, targetPath string) {
	warnings, err := scan.Rules(claudeHome.RulesDir(), targetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not scan rules files: %v\n", err)
		return
	}
	if len(warnings) > 0 {
		fmt.Println("Warning: Rules files with matching paths:")
		for _, warning := range warnings {
			fmt.Printf("  %s (line %d)\n", warning.File, warning.Line)
		}
	}
}
