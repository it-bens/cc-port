package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/ui"
)

// resolveCategoriesAndPlaceholders consumes flag-derived category selection
// (or interactive prompt fall-through, grouped by tool) and discovers
// placeholders for every target via its own Workspace.Placeholders. Shared
// by applyCategorySelection (after the --from-manifest branch) and by
// runExportManifest (whose subcommand has no --from-manifest flag).
func resolveCategoriesAndPlaceholders(
	cmd *cobra.Command, targets []tool.Target, projectPath string, banner ui.Banner,
) (selection map[string]map[string]bool, placeholders map[string][]manifest.Placeholder, err error) {
	tools := make([]tool.Tool, len(targets))
	for i, target := range targets {
		tools[i] = target.Tool
	}

	selection, err = resolveSelectionFromCmd(cmd, tools)
	if err != nil {
		return nil, nil, err
	}
	if selection == nil {
		selection, err = ui.SelectCategories(banner, tools)
		if err != nil {
			return nil, nil, err
		}
	}

	placeholders = make(map[string][]manifest.Placeholder, len(targets))
	for _, target := range targets {
		discovered, discoverErr := target.Workspace.Placeholders(projectPath, selection[target.Tool.Name()])
		if errors.Is(discoverErr, tool.ErrProjectAbsent) {
			placeholders[target.Tool.Name()] = nil
			continue
		}
		if discoverErr != nil {
			return nil, nil, fmt.Errorf("discover placeholders for %s: %w", target.Tool.Name(), discoverErr)
		}
		placeholders[target.Tool.Name()] = discovered
	}
	return selection, placeholders, nil
}

func applyCategorySelection(
	cmd *cobra.Command, targets []tool.Target, projectPath string, banner ui.Banner,
) (selection map[string]map[string]bool, placeholders map[string][]manifest.Placeholder, err error) {
	fromManifest, _ := cmd.Flags().GetString("from-manifest")

	if fromManifest != "" {
		var conflicts []string
		if cmd.Flags().Changed("all") {
			conflicts = append(conflicts, "--all")
		}
		if cmd.Flags().Changed("include") {
			conflicts = append(conflicts, "--include")
		}
		if len(conflicts) > 0 {
			return nil, nil, fmt.Errorf(
				"--from-manifest is mutually exclusive with %s; pass one or the other",
				strings.Join(conflicts, ", "),
			)
		}
		metadata, readErr := manifest.ReadManifest(fromManifest)
		if readErr != nil {
			return nil, nil, fmt.Errorf("read manifest: %w", readErr)
		}
		return categoriesAndPlaceholdersFromManifest(metadata, targets)
	}

	return resolveCategoriesAndPlaceholders(cmd, targets, projectPath, banner)
}

// categoriesAndPlaceholdersFromManifest validates each target's manifest
// block (a target absent from the manifest gets an empty selection) and
// returns the merged per-tool selection and placeholders.
func categoriesAndPlaceholdersFromManifest(
	metadata *manifest.Metadata, targets []tool.Target,
) (selection map[string]map[string]bool, placeholders map[string][]manifest.Placeholder, err error) {
	selection = make(map[string]map[string]bool, len(targets))
	placeholders = make(map[string][]manifest.Placeholder, len(targets))
	for _, target := range targets {
		name := target.Tool.Name()
		block, ok := metadata.ToolBlock(name)
		if !ok {
			selection[name] = map[string]bool{}
			continue
		}
		selected, applyErr := manifest.ApplyToolCategories(name, categoryNames(target.Tool), block.Categories)
		if applyErr != nil {
			return nil, nil, fmt.Errorf("categories from manifest for %s: %w", name, applyErr)
		}
		selection[name] = selected
		placeholders[name] = block.Placeholders
	}
	return selection, placeholders, nil
}

func categoryNames(t tool.Tool) []string {
	categories := t.Categories()
	names := make([]string, len(categories))
	for i, category := range categories {
		names[i] = category.Name
	}
	return names
}
