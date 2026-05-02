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
	"github.com/it-bens/cc-port/internal/ui"
)

// newImportCmd returns the import subcommand with closure-scoped flag
// locals. claudeDir points at the persistent root flag's local; cobra
// populates it on flag parse, so the RunE closure must dereference at
// call time.
func newImportCmd(claudeDir *string) *cobra.Command {
	var (
		fromManifest   string
		resolutionKV   []string
		passphraseEnv  string
		passphraseFile string
	)
	cmd := &cobra.Command{
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

			claudeHome, err := claude.NewHome(*claudeDir)
			if err != nil {
				return err
			}

			flagResolutions, err := parseResolutionFlags(resolutionKV)
			if err != nil {
				return err
			}

			passphrase, err := resolvePassphrase(passphraseEnv, passphraseFile)
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
			if fromManifest != "" {
				metadata, err := manifest.ReadManifest(fromManifest)
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

			result, err := importer.Run(cmd.Context(), claudeHome, importOptions)
			if err != nil {
				return fmt.Errorf("import: %w", err)
			}

			fmt.Printf("Imported to %s\n", targetPath)

			renderRulesReport(cmd.ErrOrStderr(), "", result.RulesReport)

			return nil
		},
	}
	cmd.Flags().StringVar(
		&fromManifest, "from-manifest", "",
		"path to a manifest XML file with pre-filled resolutions",
	)
	cmd.Flags().StringArrayVar(
		&resolutionKV, "resolution", nil,
		"resolve a placeholder non-interactively (repeatable; KEY=VALUE, "+
			"e.g. --resolution '{{HOME}}=/Users/me'). When combined with "+
			"--from-manifest, flag values win per key.",
	)
	cmd.Flags().StringVar(
		&passphraseEnv, "passphrase-env", "",
		"name of the environment variable holding the passphrase "+
			"(mutually exclusive with --passphrase-file)",
	)
	cmd.Flags().StringVar(
		&passphraseFile, "passphrase-file", "",
		"path to a file holding the passphrase, trailing newlines trimmed "+
			"(mutually exclusive with --passphrase-env)",
	)
	cmd.MarkFlagsMutuallyExclusive("passphrase-env", "passphrase-file")

	cmd.AddCommand(newImportManifestCmd())
	return cmd
}

// newImportManifestCmd returns the `import manifest` subcommand. Output
// and passphrase flag values live as closure-scoped locals on the cmd;
// runImportManifest reads them via cmd.Flags().GetString.
func newImportManifestCmd() *cobra.Command {
	var (
		output         string
		passphraseEnv  string
		passphraseFile string
	)
	cmd := &cobra.Command{
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
	cmd.Flags().StringVarP(&output, "output", "o", "manifest.xml",
		"path to write the manifest XML")
	cmd.Flags().StringVar(&passphraseEnv, "passphrase-env", "",
		"name of the environment variable holding the passphrase "+
			"(mutually exclusive with --passphrase-file)")
	cmd.Flags().StringVar(&passphraseFile, "passphrase-file", "",
		"path to a file holding the passphrase, trailing newlines trimmed "+
			"(mutually exclusive with --passphrase-env)")
	cmd.MarkFlagsMutuallyExclusive("passphrase-env", "passphrase-file")
	return cmd
}

// runImportManifest is the import manifest subcommand body, extracted so
// tests can drive it without re-wiring the whole cobra tree. Refuses to
// overwrite an existing output file; the user deletes it or picks a
// different path with --output.
func runImportManifest(cmd *cobra.Command, args []string) (err error) {
	output, _ := cmd.Flags().GetString("output")
	passphraseEnv, _ := cmd.Flags().GetString("passphrase-env")
	passphraseFile, _ := cmd.Flags().GetString("passphrase-file")

	archivePath := args[0]

	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("%s already exists; remove it or pass --output", output)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", output, err)
	}

	passphrase, err := resolvePassphrase(passphraseEnv, passphraseFile)
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

	if err := manifest.WriteManifest(output, metadata); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	fmt.Printf("Manifest written to %s\n", output)
	fmt.Println("Edit the resolve attributes and use --from-manifest to import.")
	return nil
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
