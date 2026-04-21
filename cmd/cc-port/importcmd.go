package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/scan"
	"github.com/it-bens/cc-port/internal/ui"
)

var (
	importFromManifest string
	importResolutionKV []string
)

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

		flagResolutions, err := parseResolutionFlags(importResolutionKV)
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
			for key, value := range flagResolutions {
				resolutions[key] = value
			}
		} else {
			resolutions, err = promptImportResolutions(archivePath, targetPath, flagResolutions)
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
	importCmd.Flags().StringArrayVar(
		&importResolutionKV, "resolution", nil,
		"resolve a placeholder non-interactively (repeatable; KEY=VALUE, "+
			"e.g. --resolution '{{HOME}}=/Users/me'). When combined with "+
			"--from-manifest, flag values win per key.",
	)
	importCmd.AddCommand(importManifestCmd)
	rootCmd.AddCommand(importCmd)
}

func promptImportResolutions(
	archivePath, targetPath string,
	preResolved map[string]string,
) (map[string]string, error) {
	metadata, err := manifest.ReadManifestFromZip(archivePath)
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

func resolutionsFromManifest(metadata *manifest.Metadata, targetPath string) map[string]string {
	resolutions := make(map[string]string)
	for _, placeholder := range metadata.Placeholders {
		if placeholder.Key == importer.ProjectPathKey {
			resolutions[placeholder.Key] = targetPath
			continue
		}
		resolutions[placeholder.Key] = placeholder.Resolve
	}
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
				"--resolution %q: expected KEY=VALUE (no '=' found)", entry,
			)
		}
		key := entry[:equalsIndex]
		value := entry[equalsIndex+1:]
		if key == "" {
			return nil, fmt.Errorf("--resolution %q: empty key", entry)
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
