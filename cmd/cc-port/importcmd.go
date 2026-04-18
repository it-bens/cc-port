package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/scan"
	"github.com/it-bens/cc-port/internal/ui"
)

var importFromManifest string

var importCmd = &cobra.Command{
	Use:   "import <archive.zip> <target-path>",
	Short: "Import a project from a cc-port ZIP archive",
	Long:  "Imports Claude Code project data from a ZIP archive into the given target path.",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		archivePath := args[0]
		targetPath, err := claude.ResolveProjectPath(args[1])
		if err != nil {
			return fmt.Errorf("resolve target path: %w", err)
		}

		claudeHome, err := claude.NewHome(claudeDir)
		if err != nil {
			return err
		}

		var resolutions map[string]string

		if importFromManifest != "" {
			metadata, err := manifest.ReadManifest(importFromManifest)
			if err != nil {
				return fmt.Errorf("read manifest: %w", err)
			}
			resolutions = resolutionsFromManifest(metadata, targetPath)
		} else {
			resolutions, err = promptImportResolutions(archivePath, targetPath)
			if err != nil {
				return err
			}
		}

		importOptions := importer.Options{
			ArchivePath: archivePath,
			TargetPath:  targetPath,
			Resolutions: resolutions,
		}

		if err := importer.Run(claudeHome, importOptions); err != nil {
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
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		archivePath := args[0]

		metadata, err := manifest.ReadManifestFromZip(archivePath)
		if err != nil {
			return fmt.Errorf("read manifest from zip: %w", err)
		}

		// Clear all resolve fields so the user fills them in manually.
		for i := range metadata.Placeholders {
			metadata.Placeholders[i].Resolve = ""
		}

		outputPath := "manifest.xml"
		if err := manifest.WriteManifest(outputPath, metadata); err != nil {
			return fmt.Errorf("write manifest: %w", err)
		}

		fmt.Printf("Manifest written to %s\n", outputPath)
		fmt.Println("Edit the resolve attributes and use --from-manifest to import.")
		return nil
	},
}

func init() {
	importCmd.Flags().StringVar(
		&importFromManifest, "from-manifest", "",
		"path to a manifest XML file with pre-filled resolutions",
	)
	importCmd.AddCommand(importManifestCmd)
	rootCmd.AddCommand(importCmd)
}

func promptImportResolutions(archivePath, targetPath string) (map[string]string, error) {
	metadata, err := manifest.ReadManifestFromZip(archivePath)
	if err != nil {
		return nil, fmt.Errorf("read manifest from zip: %w", err)
	}

	resolutions := make(map[string]string)

	for _, placeholder := range metadata.Placeholders {
		// Pre-fill PROJECT_PATH with targetPath.
		if placeholder.Key == "{{PROJECT_PATH}}" {
			resolutions[placeholder.Key] = targetPath
			continue
		}

		// Use the pre-existing resolve value if present.
		if placeholder.Resolve != "" {
			resolutions[placeholder.Key] = placeholder.Resolve
			continue
		}

		// Prompt for the remaining ones.
		resolved, err := ui.ResolvePlaceholder(placeholder.Key, placeholder.Original, "")
		if err != nil {
			return nil, err
		}
		resolutions[placeholder.Key] = resolved
	}

	return resolutions, nil
}

func resolutionsFromManifest(metadata *manifest.Metadata, targetPath string) map[string]string {
	resolutions := make(map[string]string)
	for _, placeholder := range metadata.Placeholders {
		if placeholder.Key == "{{PROJECT_PATH}}" {
			resolutions[placeholder.Key] = targetPath
			continue
		}
		resolutions[placeholder.Key] = placeholder.Resolve
	}
	return resolutions
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
