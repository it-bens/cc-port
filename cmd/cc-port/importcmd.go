package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/file"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/tool"
)

// newImportCmd returns the import subcommand with closure-scoped flag locals.
func newImportCmd(toolSet *tool.Set, flags *toolFlags) *cobra.Command {
	var (
		fromManifest   string
		passphraseEnv  string
		passphraseFile string
	)
	cmd := &cobra.Command{
		Use:   "import <archive.zip> <target-path>",
		Short: "Import a project from a cc-port ZIP archive",
		Long:  "Imports project data across every selected tool from a ZIP archive into the given target path.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(2)(cmd, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			archivePath := args[0]
			targetPath, err := tool.ResolveProjectPath(args[1])
			if err != nil {
				return fmt.Errorf("resolve target path: %w", err)
			}

			targets, err := resolveTargets(toolSet, flags)
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

			var fromManifestMeta *manifest.Metadata
			if fromManifest != "" {
				fromManifestMeta, err = manifest.ReadManifest(fromManifest)
				if err != nil {
					return fmt.Errorf("read manifest: %w", err)
				}
			}

			importOptions := importer.Options{
				Source:       source.ReaderAt,
				Size:         source.Size,
				TargetPath:   targetPath,
				Caps:         archive.DefaultCaps(),
				FromManifest: fromManifestMeta,
			}

			var result *importer.Result
			if err := runWithProgress(cmd, func(ctx context.Context, reporter progress.Reporter) error {
				importOptions.Reporter = reporter
				runResult, runErr := importer.Run(ctx, toolSet, targets, &importOptions)
				if runErr != nil {
					return fmt.Errorf("import: %w", runErr)
				}
				result = runResult
				return nil
			}); err != nil {
				return err
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Imported to %s\n", targetPath); err != nil {
				return fmt.Errorf("write success line: %w", err)
			}
			if len(result.SkippedTools) > 0 {
				if _, err := fmt.Fprintf(
					cmd.ErrOrStderr(), "note: archive has no data for: %s\n", strings.Join(result.SkippedTools, ", "),
				); err != nil {
					return fmt.Errorf("write skipped-tools note: %w", err)
				}
			}
			if err := renderImportWarnings(cmd.ErrOrStderr(), targets, result.Warnings); err != nil {
				return err
			}
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

func renderImportWarnings(stderr io.Writer, targets []tool.Target, warningsByTool map[string][]string) error {
	multi := len(targets) > 1
	for _, target := range targets {
		for _, warning := range warningsByTool[target.Tool.Name()] {
			prefix := "Warning: "
			if multi {
				prefix = fmt.Sprintf("Warning (%s): ", target.Tool.DisplayName())
			}
			if _, err := fmt.Fprintf(stderr, "%s%s\n", prefix, warning); err != nil {
				return fmt.Errorf("write import warning: %w", err)
			}
		}
	}
	return nil
}

// newImportManifestCmd returns the `import manifest` subcommand.
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

// runImportManifest is the import manifest subcommand body. Refuses to
// overwrite an existing output file.
func runImportManifest(cmd *cobra.Command, args []string) (err error) {
	output, _ := cmd.Flags().GetString("output")
	passphraseEnv, _ := cmd.Flags().GetString("passphrase-env")
	passphraseFile, _ := cmd.Flags().GetString("passphrase-file")

	archivePath := args[0]

	if err := requireOutputAbsent(output); err != nil {
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

	metadata, err := manifest.ReadManifestFromZip(source.ReaderAt, source.Size, archive.DefaultCaps().MaxEntries)
	if err != nil {
		return fmt.Errorf("read manifest from zip: %w", err)
	}

	for t := range metadata.Tools {
		for p := range metadata.Tools[t].Placeholders {
			metadata.Tools[t].Placeholders[p].Resolve = ""
		}
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
