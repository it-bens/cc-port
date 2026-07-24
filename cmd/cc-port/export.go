package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/file"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/ui"
)

// newExportCmd returns the export subcommand with closure-scoped flag
// locals. The export manifest subcommand is attached here so the caller
// wires both with one AddCommand.
func newExportCmd(toolSet *tool.Set, flags *toolFlags, banner Banner) *cobra.Command {
	var (
		output         string
		fromManifest   string
		passphraseEnv  string
		passphraseFile string
	)
	cmd := &cobra.Command{
		Use:   "export <project-path>",
		Short: "Export a project to a portable ZIP archive",
		Long:  "Exports project data across every selected tool to a ZIP archive with path anonymization.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			projectPath, err := tool.ResolveProjectPath(args[0])
			if err != nil {
				return fmt.Errorf("resolve project path: %w", err)
			}

			targets, err := resolveTargets(toolSet, flags)
			if err != nil {
				return err
			}

			selection, placeholders, err := applyCategorySelection(cmd, targets, projectPath, banner)
			if err != nil {
				return err
			}

			passphrase, err := resolvePassphrase(passphraseEnv, passphraseFile)
			if err != nil {
				return err
			}

			var result export.Result
			if err := runWithProgress(cmd, func(ctx context.Context, reporter progress.Reporter) error {
				exportOptions := export.Options{
					ProjectPath:  projectPath,
					Selected:     selection,
					Placeholders: placeholders,
					Reporter:     reporter,
				}
				runResult, runErr := runExportWithStages(
					ctx, targets, &exportOptions,
					[]pipeline.WriterStage{
						&encrypt.WriterStage{Pass: passphrase},
						&file.Sink{Path: output},
					},
				)
				if runErr != nil {
					return runErr
				}
				result = runResult
				return nil
			}); err != nil {
				return err
			}

			renderToolWarnings(cmd.ErrOrStderr(), targets, result.ByTool)

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Exported to %s\n", output); err != nil {
				return fmt.Errorf("write success line: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "output file path (required for export)")
	registerCategoryFlags(cmd, "export")
	cmd.Flags().StringVar(
		&fromManifest, "from-manifest", "",
		"path to a manifest XML file to read categories and placeholders from",
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
	// MarkFlagRequired errors only when the flag name doesn't exist; "output" was registered above.
	_ = cmd.MarkFlagRequired("output")

	cmd.AddCommand(newExportManifestCmd(toolSet, flags, banner))
	return cmd
}

// renderToolWarnings prints every target's tool.ExportResult.Warnings,
// prefixed with the tool's display name when more than one target ran.
func renderToolWarnings(stderr io.Writer, targets []tool.Target, byTool map[string]tool.ExportResult) {
	multi := len(targets) > 1
	for _, target := range targets {
		toolResult := byTool[target.Tool.Name()]
		for _, warning := range toolResult.Warnings {
			if multi {
				fmt.Fprintf(stderr, "Warning (%s): %s\n", target.Tool.DisplayName(), warning) //nolint:errcheck // best-effort stderr diagnostic
			} else {
				fmt.Fprintf(stderr, "Warning: %s\n", warning) //nolint:errcheck // best-effort stderr diagnostic
			}
		}
	}
}

// runExportWithStages composes a writer pipeline from stages, hands it to
// export.Run via Options.Output, and surfaces any close-time error on the
// pipeline writer through a "close output pipeline" wrap.
func runExportWithStages(
	ctx context.Context, targets []tool.Target,
	exportOptions *export.Options, stages []pipeline.WriterStage,
) (result export.Result, err error) {
	writer, err := pipeline.RunWriter(ctx, stages)
	if err != nil {
		return export.Result{}, fmt.Errorf("build output pipeline: %w", err)
	}
	defer func() {
		if cerr := writer.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close output pipeline: %w", cerr))
		}
	}()

	exportOptions.Output = writer

	result, err = export.Run(ctx, targets, exportOptions)
	if err != nil {
		return result, fmt.Errorf("export: %w", err)
	}
	return result, nil
}

// newExportManifestCmd returns the `export manifest` subcommand.
func newExportManifestCmd(toolSet *tool.Set, flags *toolFlags, banner ui.Banner) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "manifest <project-path>",
		Short: "Write a manifest XML for an export without creating the ZIP",
		Long:  "Discovers placeholders and categories for the project and writes a manifest XML file.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExportManifest(cmd, args, toolSet, flags, banner)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "manifest.xml",
		"path to write the manifest XML")
	registerCategoryFlags(cmd, "include")
	return cmd
}

// runExportManifest is the export manifest subcommand body. Refuses to
// overwrite an existing output file.
func runExportManifest(cmd *cobra.Command, args []string, toolSet *tool.Set, flags *toolFlags, banner ui.Banner) error {
	output, _ := cmd.Flags().GetString("output")
	if err := requireOutputAbsent(output); err != nil {
		return err
	}

	projectPath, err := tool.ResolveProjectPath(args[0])
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}

	targets, err := resolveTargets(toolSet, flags)
	if err != nil {
		return err
	}

	selection, placeholders, err := resolveCategoriesAndPlaceholders(cmd, targets, projectPath, banner)
	if err != nil {
		return err
	}

	metadata := &manifest.Metadata{}
	for _, target := range targets {
		name := target.Tool.Name()
		metadata.Tools = append(metadata.Tools, manifest.Tool{
			Name:         name,
			Categories:   manifest.BuildToolCategoryEntries(tool.CategoryNames(target.Tool), selection[name]),
			Placeholders: placeholders[name],
		})
	}

	if err := manifest.WriteManifest(output, metadata); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Manifest written to %s\n", output); err != nil {
		return fmt.Errorf("write success line: %w", err)
	}
	return nil
}
