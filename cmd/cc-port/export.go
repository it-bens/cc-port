package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/file"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/scan"
	"github.com/it-bens/cc-port/internal/ui"
)

var (
	exportOutput         string
	exportFromManifest   string
	exportManifestOutput string
)

// categoryFlags are the per-category booleans the export command
// accepts. Defined once so parseExportOptions and the conflict check
// share the list.
var categoryFlags = []string{
	"all", "sessions", "memory", "history", "file-history",
	"config", "todos", "usage-data", "plugins-data", "tasks",
}

var exportCmd = &cobra.Command{
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

		claudeHome, err := claude.NewHome(claudeDir)
		if err != nil {
			return err
		}

		var placeholders []manifest.Placeholder
		if exportOptions.FromManifest != "" {
			metadata, err := manifest.ReadManifest(exportOptions.FromManifest)
			if err != nil {
				return fmt.Errorf("read manifest: %w", err)
			}
			exportOptions.Categories, err = manifest.ApplyCategoryEntries(metadata.Export.Categories)
			if err != nil {
				return fmt.Errorf("categories from manifest: %w", err)
			}
			placeholders = metadata.Placeholders
		} else {
			placeholders, err = discoverAndPromptPlaceholders(claudeHome, exportOptions.ProjectPath)
			if err != nil {
				return err
			}
		}
		exportOptions.Placeholders = placeholders

		printExportRulesWarnings(claudeHome, exportOptions.ProjectPath)

		result, err := runExportWithStages(
			cmd.Context(), claudeHome, &exportOptions,
			[]pipeline.WriterStage{&file.Sink{Path: outputPath}},
		)
		if err != nil {
			return err
		}

		if snapshotCount := len(result.FileHistory); snapshotCount > 0 {
			fmt.Fprintf(
				os.Stderr,
				"Warning: %d file-history snapshot(s) archived as-is — "+
					"contents may still reference the original project path "+
					"(used for in-session rewinds, not persisted data)\n",
				snapshotCount,
			)
		}

		fmt.Printf("Exported to %s\n", outputPath)
		return nil
	},
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

var exportManifestCmd = &cobra.Command{
	Use:   "manifest <project-path>",
	Short: "Write a manifest XML for an export without creating the ZIP",
	Long:  "Discovers placeholders and categories for the project and writes a manifest XML file.",
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return &usageError{err: err}
		}
		return nil
	},
	RunE: runExportManifest,
}

// runExportManifest is the export manifest subcommand body, extracted so
// tests can drive it without re-wiring the whole cobra tree. Refuses to
// overwrite an existing output file; the user deletes it or picks a
// different path with --output. The guard runs before placeholder
// discovery so the user is not asked to answer prompts only to fail on
// the pre-existing output.
func runExportManifest(cmd *cobra.Command, args []string) error {
	if _, err := os.Stat(exportManifestOutput); err == nil {
		return fmt.Errorf("%s already exists; remove it or pass --output", exportManifestOutput)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", exportManifestOutput, err)
	}

	projectPath, err := claude.ResolveProjectPath(args[0])
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}

	claudeHome, err := claude.NewHome(claudeDir)
	if err != nil {
		return err
	}

	categories, err := resolveExportCategories(cmd)
	if err != nil {
		return err
	}

	placeholders, err := discoverAndPromptPlaceholders(claudeHome, projectPath)
	if err != nil {
		return err
	}

	printExportRulesWarnings(claudeHome, projectPath)

	exportOptions := export.Options{
		ProjectPath:  projectPath,
		Categories:   categories,
		Placeholders: placeholders,
	}

	metadata := buildExportMetadata(&exportOptions)

	if err := manifest.WriteManifest(exportManifestOutput, metadata); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	fmt.Printf("Manifest written to %s\n", exportManifestOutput)
	return nil
}

func init() {
	exportCmd.Flags().StringVarP(&exportOutput, "output", "o", "", "output file path (required for export)")
	exportCmd.Flags().Bool("all", false, "export all categories")
	exportCmd.Flags().Bool("sessions", false, "export sessions")
	exportCmd.Flags().Bool("memory", false, "export memory")
	exportCmd.Flags().Bool("history", false, "export history")
	exportCmd.Flags().Bool("file-history", false, "export file history")
	exportCmd.Flags().Bool("config", false, "export config")
	exportCmd.Flags().StringVar(
		&exportFromManifest, "from-manifest", "",
		"path to a manifest XML file to read categories and placeholders from",
	)
	exportCmd.Flags().Bool("todos", false, "export todos")
	exportCmd.Flags().Bool("usage-data", false, "export usage-data (session-meta + facets)")
	exportCmd.Flags().Bool("plugins-data", false, "export plugins/data")
	exportCmd.Flags().Bool("tasks", false, "export tasks")
	// MarkFlagRequired errors only when the flag name doesn't exist; "output" was registered above.
	_ = exportCmd.MarkFlagRequired("output")

	exportManifestCmd.Flags().StringVarP(
		&exportManifestOutput, "output", "o", "manifest.xml",
		"path to write the manifest XML",
	)
	exportManifestCmd.Flags().Bool("all", false, "include all categories")
	exportManifestCmd.Flags().Bool("sessions", false, "include sessions")
	exportManifestCmd.Flags().Bool("memory", false, "include memory")
	exportManifestCmd.Flags().Bool("history", false, "include history")
	exportManifestCmd.Flags().Bool("file-history", false, "include file history")
	exportManifestCmd.Flags().Bool("config", false, "include config")
	exportManifestCmd.Flags().Bool("todos", false, "include todos")
	exportManifestCmd.Flags().Bool("usage-data", false, "include usage-data")
	exportManifestCmd.Flags().Bool("plugins-data", false, "include plugins/data")
	exportManifestCmd.Flags().Bool("tasks", false, "include tasks")

	exportCmd.AddCommand(exportManifestCmd)
	rootCmd.AddCommand(exportCmd)
}

// parseExportOptions turns the cobra command + positional args into an
// export.Options and the destination path the cmd-layer uses to compose
// the output pipeline. Pure: does not load manifests or discover
// placeholders. Callers handle those side effects based on the returned
// FromManifest value.
func parseExportOptions(cmd *cobra.Command, args []string) (export.Options, string, error) {
	projectPath, err := claude.ResolveProjectPath(args[0])
	if err != nil {
		return export.Options{}, "", fmt.Errorf("resolve project path: %w", err)
	}
	output, _ := cmd.Flags().GetString("output")
	fromManifest, _ := cmd.Flags().GetString("from-manifest")

	if fromManifest != "" {
		var conflicts []string
		for _, name := range categoryFlags {
			if cmd.Flags().Changed(name) {
				conflicts = append(conflicts, "--"+name)
			}
		}
		if len(conflicts) > 0 {
			return export.Options{}, "", fmt.Errorf(
				"--from-manifest is mutually exclusive with %s; pass one or the other",
				strings.Join(conflicts, ", "),
			)
		}
	}

	var categories manifest.CategorySet
	if fromManifest == "" {
		categories, err = resolveExportCategories(cmd)
		if err != nil {
			return export.Options{}, "", err
		}
	}
	return export.Options{
		ProjectPath:  projectPath,
		Categories:   categories,
		FromManifest: fromManifest,
	}, output, nil
}

func resolveExportCategories(cmd *cobra.Command) (manifest.CategorySet, error) {
	all, _ := cmd.Flags().GetBool("all")
	if all {
		return manifest.CategorySet{
			Sessions: true, Memory: true, History: true, FileHistory: true, Config: true,
			Todos: true, UsageData: true, PluginsData: true, Tasks: true,
		}, nil
	}

	sessions, _ := cmd.Flags().GetBool("sessions")
	memory, _ := cmd.Flags().GetBool("memory")
	history, _ := cmd.Flags().GetBool("history")
	fileHistory, _ := cmd.Flags().GetBool("file-history")
	config, _ := cmd.Flags().GetBool("config")
	todos, _ := cmd.Flags().GetBool("todos")
	usageData, _ := cmd.Flags().GetBool("usage-data")
	pluginsData, _ := cmd.Flags().GetBool("plugins-data")
	tasks, _ := cmd.Flags().GetBool("tasks")

	anyExplicit := sessions || memory || history || fileHistory || config ||
		todos || usageData || pluginsData || tasks
	if anyExplicit {
		return manifest.CategorySet{
			Sessions:    sessions,
			Memory:      memory,
			History:     history,
			FileHistory: fileHistory,
			Config:      config,
			Todos:       todos,
			UsageData:   usageData,
			PluginsData: pluginsData,
			Tasks:       tasks,
		}, nil
	}

	return ui.SelectCategories()
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

// printExportRulesWarnings scans the rules directory for references to projectPath
// and prints any warnings found.
func printExportRulesWarnings(claudeHome *claude.Home, projectPath string) {
	warnings, err := scan.Rules(claudeHome.RulesDir(), projectPath)
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
