package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/ui"
)

// resolveCategoriesAndPlaceholders consumes flag-derived category selection
// (or interactive prompt fall-through) and discovers placeholders. Shared
// by applyCategorySelection (after the --from-manifest branch) and by
// runExportManifest (whose subcommand has no --from-manifest flag).
func resolveCategoriesAndPlaceholders(
	cmd *cobra.Command, claudeHome *claude.Home, projectPath string, banner ui.Banner,
) (manifest.CategorySet, []manifest.Placeholder, error) {
	categories, err := resolveCategoriesFromCmd(cmd)
	if err != nil {
		return manifest.CategorySet{}, nil, err
	}
	anySet := false
	for _, spec := range manifest.AllCategories {
		if spec.Value(&categories) {
			anySet = true
			break
		}
	}
	if !anySet {
		categories, err = ui.SelectCategories(banner)
		if err != nil {
			return manifest.CategorySet{}, nil, err
		}
	}
	placeholders, err := discoverAndPromptPlaceholders(claudeHome, projectPath)
	if err != nil {
		return manifest.CategorySet{}, nil, err
	}
	return categories, placeholders, nil
}

func applyCategorySelection(
	cmd *cobra.Command, claudeHome *claude.Home, projectPath string, banner ui.Banner,
) (manifest.CategorySet, []manifest.Placeholder, error) {
	fromManifest, _ := cmd.Flags().GetString("from-manifest")

	if fromManifest != "" {
		var conflicts []string
		if cmd.Flags().Changed("all") {
			conflicts = append(conflicts, "--all")
		}
		for _, spec := range manifest.AllCategories {
			if cmd.Flags().Changed(spec.Name) {
				conflicts = append(conflicts, "--"+spec.Name)
			}
		}
		if len(conflicts) > 0 {
			return manifest.CategorySet{}, nil, fmt.Errorf(
				"--from-manifest is mutually exclusive with %s; pass one or the other",
				strings.Join(conflicts, ", "),
			)
		}
		metadata, err := manifest.ReadManifest(fromManifest)
		if err != nil {
			return manifest.CategorySet{}, nil, fmt.Errorf("read manifest: %w", err)
		}
		categories, err := manifest.ApplyCategoryEntries(metadata.Export.Categories)
		if err != nil {
			return manifest.CategorySet{}, nil, fmt.Errorf("categories from manifest: %w", err)
		}
		return categories, metadata.Placeholders, nil
	}

	return resolveCategoriesAndPlaceholders(cmd, claudeHome, projectPath, banner)
}
