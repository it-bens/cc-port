package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/file"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
)

// newImportCmd returns the import subcommand with closure-scoped flag
// locals. claudeDir points at the persistent root flag's local; cobra
// populates it on flag parse, so the RunE closure must dereference at
// call time.
func newImportCmd(claudeDir *string) *cobra.Command {
	var (
		fromManifest   string
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

			passphrase, err := resolvePassphrase(passphraseEnv, passphraseFile)
			if err != nil {
				return err
			}

			source, err := pipeline.RunReader(cmd.Context(), []pipeline.ReaderStage{
				&file.Source{Path: archivePath},
				&encrypt.ReaderStage{Pass: passphrase, Mode: encrypt.Strict},
				&pipeline.MaterializeStage{},
			})
			if err != nil {
				return fmt.Errorf("open archive source: %w", err)
			}
			defer func() {
				if cerr := source.Close(); cerr != nil {
					err = errors.Join(err, fmt.Errorf("close archive source: %w", cerr))
				}
			}()

			resolutions, err := composeImportResolutions(source, fromManifest)
			if err != nil {
				return err
			}

			homePath, err := resolveHomeAnchor()
			if err != nil {
				return err
			}

			importOptions := importer.Options{
				Source:      source.ReaderAt,
				Size:        source.Size,
				TargetPath:  targetPath,
				HomePath:    homePath,
				Resolutions: resolutions,
			}

			result, err := importer.Run(cmd.Context(), claudeHome, importOptions)
			if err != nil {
				return fmt.Errorf("import: %w", err)
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Imported to %s\n", targetPath); err != nil {
				return fmt.Errorf("write success line: %w", err)
			}

			renderRulesReport(cmd.ErrOrStderr(), "", result.RulesReport)

			return nil
		},
	}
	cmd.Flags().StringVar(
		&fromManifest, "from-manifest", "",
		"path to a manifest XML file with pre-filled resolutions",
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
		&pipeline.MaterializeStage{},
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

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Manifest written to %s\n", output); err != nil {
		return fmt.Errorf("write success line: %w", err)
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Edit the resolve attributes and use --from-manifest to import."); err != nil {
		return fmt.Errorf("write follow-up hint: %w", err)
	}
	return nil
}

// composeImportResolutions reads the archive's manifest, optionally loads a
// --from-manifest override, and delegates the merge to
// importer.ResolvePlaceholders. Refuses imports with unresolved non-implicit
// keys via MissingResolutionsError.
func composeImportResolutions(
	source pipeline.Source, fromManifest string,
) (map[string]string, error) {
	metadata, err := manifest.ReadManifestFromZip(source.ReaderAt, source.Size)
	if err != nil {
		return nil, fmt.Errorf("read manifest from zip: %w", err)
	}

	var fromManifestMeta *manifest.Metadata
	if fromManifest != "" {
		fromManifestMeta, err = manifest.ReadManifest(fromManifest)
		if err != nil {
			return nil, fmt.Errorf("read manifest: %w", err)
		}
	}

	unresolved := make([]string, 0, len(metadata.Placeholders))
	for _, placeholder := range metadata.Placeholders {
		unresolved = append(unresolved, placeholder.Key)
	}

	return importer.ResolvePlaceholders(unresolved, fromManifestMeta)
}
