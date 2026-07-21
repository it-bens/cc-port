package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/tool"
)

// registerCategoryFlags registers --all plus the repeatable --include flag
// on cmd. verb is woven into each flag's help text.
func registerCategoryFlags(cmd *cobra.Command, verb string) {
	cmd.Flags().Bool("all", false, fmt.Sprintf("%s every category for every selected tool", verb))
	cmd.Flags().StringArray(
		"include", nil,
		fmt.Sprintf(`%s only "<tool>/<category>" (repeatable; bare category names are rejected)`, verb),
	)
}

// resolveSelectionFromCmd reads --all and --include, registered by
// registerCategoryFlags, into a per-tool category selection. --all selects
// every category for every target; --include selects only the named
// tool/category pairs (every other target starts from an empty selection).
// Neither flag present returns nil so callers (export and push, both via
// applyCategorySelection) fall back to the interactive picker.
func resolveSelectionFromCmd(cmd *cobra.Command, tools []tool.Tool) (map[string]map[string]bool, error) {
	all, err := cmd.Flags().GetBool("all")
	if err != nil {
		return nil, fmt.Errorf("read --all flag: %w", err)
	}
	includes, err := cmd.Flags().GetStringArray("include")
	if err != nil {
		return nil, fmt.Errorf("read --include flag: %w", err)
	}
	if all && len(includes) > 0 {
		return nil, fmt.Errorf("--all and --include are mutually exclusive")
	}
	if all {
		selection := make(map[string]map[string]bool, len(tools))
		for _, t := range tools {
			selected := make(map[string]bool, len(t.Categories()))
			for _, category := range t.Categories() {
				selected[category.Name] = true
			}
			selection[t.Name()] = selected
		}
		return selection, nil
	}

	if len(includes) == 0 {
		return nil, nil
	}

	selection := make(map[string]map[string]bool, len(tools))
	for _, t := range tools {
		selection[t.Name()] = make(map[string]bool)
	}
	for _, raw := range includes {
		qualified, err := tool.ParseQualified(raw)
		if err != nil {
			return nil, err
		}
		selected, ok := selection[qualified.Tool]
		if !ok {
			return nil, fmt.Errorf("--include %q names a tool not selected for this run", raw)
		}
		if !toolHasCategory(tools, qualified.Tool, qualified.Category) {
			return nil, fmt.Errorf(
				"--include %q names an unknown category; valid categories for %s: %s",
				raw, qualified.Tool, categoryNamesForTool(tools, qualified.Tool),
			)
		}
		selected[qualified.Category] = true
	}
	return selection, nil
}

func toolHasCategory(tools []tool.Tool, toolName, categoryName string) bool {
	for _, selectedTool := range tools {
		if selectedTool.Name() != toolName {
			continue
		}
		for _, category := range selectedTool.Categories() {
			if category.Name == categoryName {
				return true
			}
		}
	}
	return false
}

func categoryNamesForTool(tools []tool.Tool, toolName string) string {
	for _, selectedTool := range tools {
		if selectedTool.Name() != toolName {
			continue
		}
		names := make([]string, 0, len(selectedTool.Categories()))
		for _, category := range selectedTool.Categories() {
			names = append(names, category.Name)
		}
		return strings.Join(names, ", ")
	}
	return ""
}
