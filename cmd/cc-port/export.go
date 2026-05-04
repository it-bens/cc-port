package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/file"
	"github.com/it-bens/cc-port/internal/fsutil"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/scan"
	"github.com/it-bens/cc-port/internal/ui"
)

// newExportCmd returns the export subcommand with closure-scoped flag
// locals. claudeDir points at the persistent root flag's local; cobra
// populates it on flag parse, so the RunE closure must dereference at
// call time. The export manifest subcommand is attached here so the
// caller wires both with one AddCommand.
func newExportCmd(claudeDir *string) *cobra.Command {
	var (
		output         string
		fromManifest   string
		passphraseEnv  string
		passphraseFile string
	)
	cmd := &cobra.Command{
		Use:   "export <project-path>",
		Short: "Export a project to a portable ZIP archive",
		Long:  "Exports Claude Code project data to a ZIP archive with path anonymization.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			exportOptions, outputPath, err := parseExportOptions(cmd, args)
			if err != nil {
				return err
			}

			claudeHome, err := claude.NewHome(*claudeDir)
			if err != nil {
				return err
			}

			categories, placeholders, err := applyCategorySelection(cmd, claudeHome, exportOptions.ProjectPath)
			if err != nil {
				return err
			}
			exportOptions.Categories = categories
			exportOptions.Placeholders = placeholders

			passphrase, err := resolvePassphrase(passphraseEnv, passphraseFile)
			if err != nil {
				return err
			}

			result, err := runExportWithStages(
				cmd.Context(), claudeHome, &exportOptions,
				[]pipeline.WriterStage{
					&encrypt.WriterStage{Pass: passphrase},
					&file.Sink{Path: outputPath},
				},
			)
			if err != nil {
				return err
			}

			renderRulesReport(cmd.ErrOrStderr(), "", result.RulesReport)

			if snapshotCount := len(result.FileHistory); snapshotCount > 0 {
				if _, err := fmt.Fprintf(
					cmd.ErrOrStderr(),
					"Warning: %d file-history snapshot(s) archived as-is. "+
						"Contents may still reference the original project path "+
						"(used for in-session rewinds, not persisted data)\n",
					snapshotCount,
				); err != nil {
					return fmt.Errorf("write file-history warning: %w", err)
				}
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Exported to %s\n", outputPath); err != nil {
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

	cmd.AddCommand(newExportManifestCmd(claudeDir))
	return cmd
}

// runExportWithStages composes a writer pipeline from stages, hands it to
// export.Run via Options.Output, and surfaces any close-time error on the
// pipeline writer through a "close output pipeline" wrap. Extracted so a
// test can drive the close-error path with a stage that fails on Close
// without re-running the whole cobra command.
func runExportWithStages(
	ctx context.Context, claudeHome *claude.Home,
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

	result, err = export.Run(ctx, claudeHome, exportOptions)
	if err != nil {
		return result, fmt.Errorf("export: %w", err)
	}
	return result, nil
}

// newExportManifestCmd returns the `export manifest` subcommand. claudeDir
// points at the persistent root flag's local. Output and category flags
// live as closure-scoped locals on the cmd; runExportManifest reads
// --output via cmd.Flags().GetString.
func newExportManifestCmd(claudeDir *string) *cobra.Command {
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
			return runExportManifest(cmd, args, *claudeDir)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "manifest.xml",
		"path to write the manifest XML")
	registerCategoryFlags(cmd, "include")
	return cmd
}

// runExportManifest is the export manifest subcommand body, extracted so
// tests can drive it without re-wiring the whole cobra tree. Refuses to
// overwrite an existing output file; the user deletes it or picks a
// different path with --output. The guard runs before placeholder
// discovery so the user is not asked to answer prompts only to fail on
// the pre-existing output.
func runExportManifest(cmd *cobra.Command, args []string, claudeDir string) error {
	output, _ := cmd.Flags().GetString("output")
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("%s already exists; remove it or pass --output", output)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", output, err)
	}

	projectPath, err := claude.ResolveProjectPath(args[0])
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}

	claudeHome, err := claude.NewHome(claudeDir)
	if err != nil {
		return err
	}

	categories, err := resolveCategoriesFromCmd(cmd)
	if err != nil {
		return err
	}
	anySet := false
	for _, spec := range manifest.AllCategories {
		if spec.Value(&categories) {
			anySet = true
			break
		}
	}
	if !anySet {
		categories, err = ui.SelectCategories()
		if err != nil {
			return err
		}
	}

	placeholders, err := discoverAndPromptPlaceholders(claudeHome, projectPath)
	if err != nil {
		return err
	}

	renderRulesReport(cmd.ErrOrStderr(), "", scan.ScanReport(claudeHome.RulesDir(), projectPath))

	exportOptions := export.Options{
		ProjectPath:  projectPath,
		Categories:   categories,
		Placeholders: placeholders,
	}

	metadata := buildExportMetadata(&exportOptions)

	if err := manifest.WriteManifest(output, metadata); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Manifest written to %s\n", output); err != nil {
		return fmt.Errorf("write success line: %w", err)
	}
	return nil
}

// parseExportOptions resolves the project path and the output destination
// from positional args plus the --output flag. Returns a partial Options
// (ProjectPath, FromManifest) and the output path. Categories and
// Placeholders are filled in by applyCategorySelection in the caller.
func parseExportOptions(cmd *cobra.Command, args []string) (export.Options, string, error) {
	projectPath, err := claude.ResolveProjectPath(args[0])
	if err != nil {
		return export.Options{}, "", fmt.Errorf("resolve project path: %w", err)
	}
	output, _ := cmd.Flags().GetString("output")
	fromManifest, _ := cmd.Flags().GetString("from-manifest")
	return export.Options{
		ProjectPath:  projectPath,
		FromManifest: fromManifest,
	}, output, nil
}

func discoverAndPromptPlaceholders(claudeHome *claude.Home, projectPath string) ([]manifest.Placeholder, error) {
	locations, err := claude.LocateProject(claudeHome, projectPath)
	if err != nil {
		return nil, fmt.Errorf("locate project: %w", err)
	}

	contentBuffer, err := gatherProjectContent(locations)
	if err != nil {
		return nil, err
	}

	homePath, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("determine home directory: %w", err)
	}

	paths := export.DiscoverPaths(contentBuffer)
	prefixes := export.GroupPathPrefixes(paths)
	suggestions := export.AutoDetectPlaceholders(prefixes, projectPath, homePath)

	return resolveSuggestions(suggestions)
}

func gatherProjectContent(locations *claude.ProjectLocations) ([]byte, error) {
	var contentBuffer []byte

	for _, transcriptPath := range locations.SessionTranscripts {
		data, err := os.ReadFile(transcriptPath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return nil, fmt.Errorf("read transcript %s: %w", transcriptPath, err)
		}
		contentBuffer = append(contentBuffer, data...)
	}

	for _, memoryFilePath := range locations.MemoryFiles {
		data, err := os.ReadFile(memoryFilePath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return nil, fmt.Errorf("read memory file %s: %w", memoryFilePath, err)
		}
		contentBuffer = append(contentBuffer, data...)
	}

	for _, sessionFilePath := range locations.SessionFiles {
		data, err := os.ReadFile(sessionFilePath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return nil, fmt.Errorf("read session file %s: %w", sessionFilePath, err)
		}
		contentBuffer = append(contentBuffer, data...)
	}

	extra, err := gatherSessionKeyedContent(locations)
	if err != nil {
		return nil, err
	}
	contentBuffer = append(contentBuffer, extra...)

	return contentBuffer, nil
}

// gatherSessionKeyedContent reads every file yielded by AllFlatFiles() and
// returns their concatenated bytes. The iterator applies each group's
// sidecar filter, so callers do not need to exclude runtime-only
// basenames themselves.
func gatherSessionKeyedContent(locations *claude.ProjectLocations) ([]byte, error) {
	var buf []byte
	for group, path := range locations.AllFlatFiles() {
		data, err := os.ReadFile(path) //nolint:gosec // path from trusted ProjectLocations
		if err != nil {
			return nil, fmt.Errorf("read %s file %s: %w", group.Name, path, err)
		}
		buf = append(buf, data...)
	}
	return buf, nil
}

func resolveSuggestions(suggestions []export.PlaceholderSuggestion) ([]manifest.Placeholder, error) {
	placeholders := make([]manifest.Placeholder, 0, len(suggestions))
	for _, suggestion := range suggestions {
		if suggestion.Auto {
			resolvable := true
			placeholders = append(placeholders, manifest.Placeholder{
				Key:        suggestion.Key,
				Original:   suggestion.Original,
				Resolvable: &resolvable,
			})
			continue
		}

		resolved, err := ui.ResolvePlaceholder(suggestion.Key, suggestion.Original, "")
		if err != nil {
			return nil, err
		}

		resolvable := resolved != ""
		placeholders = append(placeholders, manifest.Placeholder{
			Key:        suggestion.Key,
			Original:   suggestion.Original,
			Resolvable: &resolvable,
			Resolve:    resolved,
		})
	}
	return placeholders, nil
}

func buildExportMetadata(exportOptions *export.Options) *manifest.Metadata {
	resolvableTrue := true
	placeholders := make([]manifest.Placeholder, 0, len(exportOptions.Placeholders))
	for _, placeholder := range exportOptions.Placeholders {
		placeholders = append(placeholders, manifest.Placeholder{
			Key:        placeholder.Key,
			Original:   placeholder.Original,
			Resolvable: &resolvableTrue,
			Resolve:    placeholder.Resolve,
		})
	}

	return &manifest.Metadata{
		Export: manifest.Info{
			Categories: manifest.BuildCategoryEntries(&exportOptions.Categories),
		},
		Placeholders: placeholders,
	}
}

// resolveHomeAnchor mirrors claude.ResolveProjectPath: a symlinked HOME
// must resolve to its target before the anchor filter compares against
// project paths, otherwise every home-rooted candidate is silently
// dropped. Rejecting `/` and non-absolute values prevents the anchor
// from matching every absolute path in the corpus.
func resolveHomeAnchor() (string, error) {
	homePath, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	if !filepath.IsAbs(homePath) {
		return "", fmt.Errorf("invalid home directory %q: must be absolute", homePath)
	}
	resolved, err := fsutil.ResolveExistingAncestor(homePath)
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	cleaned := filepath.Clean(resolved)
	if !filepath.IsAbs(cleaned) || cleaned == "/" {
		return "", fmt.Errorf("invalid home directory %q", cleaned)
	}
	return cleaned, nil
}
