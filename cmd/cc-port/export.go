package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/scan"
	"github.com/it-bens/cc-port/internal/ui"
)

var (
	exportOutput       string
	exportAll          bool
	exportSessions     bool
	exportMemory       bool
	exportHistory      bool
	exportFileHistory  bool
	exportConfig       bool
	exportFromManifest string
	exportTodos        bool
	exportUsageData    bool
	exportPluginsData  bool
	exportTasks        bool
)

var exportCmd = &cobra.Command{
	Use:   "export <project-path>",
	Short: "Export a project to a portable ZIP archive",
	Long:  "Exports Claude Code project data to a ZIP archive with path anonymization.",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		projectPath, err := claude.ResolveProjectPath(args[0])
		if err != nil {
			return fmt.Errorf("resolve project path: %w", err)
		}

		claudeHome, err := claude.NewHome(claudeDir)
		if err != nil {
			return err
		}

		var categories export.CategorySet
		var placeholders []export.Placeholder

		if exportFromManifest != "" {
			metadata, err := export.ReadManifest(exportFromManifest)
			if err != nil {
				return fmt.Errorf("read manifest: %w", err)
			}
			categories, err = categorizFromMetadata(metadata)
			if err != nil {
				return fmt.Errorf("categories from manifest: %w", err)
			}
			placeholders = metadata.Placeholders
		} else {
			categories, err = resolveExportCategories()
			if err != nil {
				return err
			}
			placeholders, err = discoverAndPromptPlaceholders(claudeHome, projectPath)
			if err != nil {
				return err
			}
		}

		printExportRulesWarnings(claudeHome, projectPath)

		exportOptions := export.Options{
			ProjectPath:  projectPath,
			OutputPath:   exportOutput,
			Categories:   categories,
			Placeholders: placeholders,
		}

		result, err := export.Run(claudeHome, exportOptions)
		if err != nil {
			return fmt.Errorf("export: %w", err)
		}

		if result.FileHistorySnapshotsArchived > 0 {
			fmt.Fprintf(
				os.Stderr,
				"Warning: %d file-history snapshot(s) archived as-is — "+
					"contents may still reference the original project path "+
					"(used for in-session rewinds, not persisted data)\n",
				result.FileHistorySnapshotsArchived,
			)
		}

		fmt.Printf("Exported to %s\n", exportOutput)
		return nil
	},
}

var exportManifestCmd = &cobra.Command{
	Use:   "manifest <project-path>",
	Short: "Write a manifest XML for an export without creating the ZIP",
	Long:  "Discovers placeholders and categories for the project and writes a manifest XML file.",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		projectPath, err := claude.ResolveProjectPath(args[0])
		if err != nil {
			return fmt.Errorf("resolve project path: %w", err)
		}

		claudeHome, err := claude.NewHome(claudeDir)
		if err != nil {
			return err
		}

		categories, err := resolveExportCategories()
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
			OutputPath:   exportOutput,
			Categories:   categories,
			Placeholders: placeholders,
		}

		metadata := buildExportMetadata(exportOptions)
		outputPath := exportOutput
		if outputPath == "" {
			outputPath = "manifest.xml"
		}

		if err := export.WriteManifest(outputPath, metadata); err != nil {
			return fmt.Errorf("write manifest: %w", err)
		}

		fmt.Printf("Manifest written to %s\n", outputPath)
		return nil
	},
}

func init() {
	exportCmd.Flags().StringVarP(&exportOutput, "output", "o", "", "output file path (required for export)")
	exportCmd.Flags().BoolVar(&exportAll, "all", false, "export all categories")
	exportCmd.Flags().BoolVar(&exportSessions, "sessions", false, "export sessions")
	exportCmd.Flags().BoolVar(&exportMemory, "memory", false, "export memory")
	exportCmd.Flags().BoolVar(&exportHistory, "history", false, "export history")
	exportCmd.Flags().BoolVar(&exportFileHistory, "file-history", false, "export file history")
	exportCmd.Flags().BoolVar(&exportConfig, "config", false, "export config")
	exportCmd.Flags().StringVar(
		&exportFromManifest, "from-manifest", "",
		"path to a manifest XML file to read categories and placeholders from",
	)
	exportCmd.Flags().BoolVar(&exportTodos, "todos", false, "export todos")
	exportCmd.Flags().BoolVar(&exportUsageData, "usage-data", false, "export usage-data (session-meta + facets)")
	exportCmd.Flags().BoolVar(&exportPluginsData, "plugins-data", false, "export plugins/data")
	exportCmd.Flags().BoolVar(&exportTasks, "tasks", false, "export tasks")
	_ = exportCmd.MarkFlagRequired("output")

	exportManifestCmd.Flags().StringVarP(
		&exportOutput, "output", "o", "",
		"output manifest file path (defaults to manifest.xml)",
	)
	exportManifestCmd.Flags().BoolVar(&exportAll, "all", false, "include all categories")
	exportManifestCmd.Flags().BoolVar(&exportSessions, "sessions", false, "include sessions")
	exportManifestCmd.Flags().BoolVar(&exportMemory, "memory", false, "include memory")
	exportManifestCmd.Flags().BoolVar(&exportHistory, "history", false, "include history")
	exportManifestCmd.Flags().BoolVar(&exportFileHistory, "file-history", false, "include file history")
	exportManifestCmd.Flags().BoolVar(&exportConfig, "config", false, "include config")
	exportManifestCmd.Flags().BoolVar(&exportTodos, "todos", false, "include todos")
	exportManifestCmd.Flags().BoolVar(&exportUsageData, "usage-data", false, "include usage-data")
	exportManifestCmd.Flags().BoolVar(&exportPluginsData, "plugins-data", false, "include plugins/data")
	exportManifestCmd.Flags().BoolVar(&exportTasks, "tasks", false, "include tasks")

	exportCmd.AddCommand(exportManifestCmd)
	rootCmd.AddCommand(exportCmd)
}

func resolveExportCategories() (export.CategorySet, error) {
	if exportAll {
		return export.CategorySet{
			Sessions: true, Memory: true, History: true, FileHistory: true, Config: true,
			Todos: true, UsageData: true, PluginsData: true, Tasks: true,
		}, nil
	}

	anyExplicit := exportSessions || exportMemory || exportHistory || exportFileHistory || exportConfig ||
		exportTodos || exportUsageData || exportPluginsData || exportTasks
	if anyExplicit {
		return export.CategorySet{
			Sessions:    exportSessions,
			Memory:      exportMemory,
			History:     exportHistory,
			FileHistory: exportFileHistory,
			Config:      exportConfig,
			Todos:       exportTodos,
			UsageData:   exportUsageData,
			PluginsData: exportPluginsData,
			Tasks:       exportTasks,
		}, nil
	}

	return ui.SelectCategories()
}

func discoverAndPromptPlaceholders(claudeHome *claude.Home, projectPath string) ([]export.Placeholder, error) {
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

func resolveSuggestions(suggestions []export.PlaceholderSuggestion) ([]export.Placeholder, error) {
	var placeholders []export.Placeholder
	for _, suggestion := range suggestions {
		if suggestion.Auto {
			resolvable := true
			placeholders = append(placeholders, export.Placeholder{
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
		placeholders = append(placeholders, export.Placeholder{
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

func categorizFromMetadata(metadata *export.Metadata) (export.CategorySet, error) {
	var categories export.CategorySet
	for _, category := range metadata.Export.Categories {
		switch category.Name {
		case "sessions":
			categories.Sessions = category.Included
		case "memory":
			categories.Memory = category.Included
		case "history":
			categories.History = category.Included
		case "file-history":
			categories.FileHistory = category.Included
		case "config":
			categories.Config = category.Included
		case "todos":
			categories.Todos = category.Included
		case "usage-data":
			categories.UsageData = category.Included
		case "plugins-data":
			categories.PluginsData = category.Included
		case "tasks":
			categories.Tasks = category.Included
		default:
			return export.CategorySet{}, fmt.Errorf("unknown category %q in manifest", category.Name)
		}
	}
	return categories, nil
}

func buildExportMetadata(exportOptions export.Options) *export.Metadata {
	resolvableTrue := true
	var placeholders []export.Placeholder
	for _, placeholder := range exportOptions.Placeholders {
		placeholders = append(placeholders, export.Placeholder{
			Key:        placeholder.Key,
			Original:   placeholder.Original,
			Resolvable: &resolvableTrue,
			Resolve:    placeholder.Resolve,
		})
	}

	return &export.Metadata{
		Export: export.Info{
			Categories: []export.Category{
				{Name: "sessions", Included: exportOptions.Categories.Sessions},
				{Name: "memory", Included: exportOptions.Categories.Memory},
				{Name: "history", Included: exportOptions.Categories.History},
				{Name: "file-history", Included: exportOptions.Categories.FileHistory},
				{Name: "config", Included: exportOptions.Categories.Config},
				{Name: "todos", Included: exportOptions.Categories.Todos},
				{Name: "usage-data", Included: exportOptions.Categories.UsageData},
				{Name: "plugins-data", Included: exportOptions.Categories.PluginsData},
				{Name: "tasks", Included: exportOptions.Categories.Tasks},
			},
		},
		Placeholders: placeholders,
	}
}
