package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/ui"
)

// applyCategorySelection translates cmd flags into a category set plus a
// placeholder slice. It enforces --from-manifest exclusivity with --all
// and per-category flags, reads the manifest when --from-manifest is set,
// and falls through to flag-derived selection or the interactive prompt
// otherwise.
//
// Used by both newExportCmd and newPushCmd. Reads --from-manifest via
// cmd.Flags().GetString so neither caller needs a package-level flag var.
func applyCategorySelection(
	cmd *cobra.Command, claudeHome *claude.Home, projectPath string,
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
		categories, err = ui.SelectCategories()
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
