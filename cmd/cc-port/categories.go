package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/manifest"
)

// registerCategoryFlags registers --all plus one boolean flag per
// manifest.AllCategories entry on cmd. verb is woven into each flag's
// help text. Iteration over manifest.AllCategories is the
// registry-of-truth (internal/manifest/AGENTS.md); no parallel
// category list lives in cmd/cc-port.
func registerCategoryFlags(cmd *cobra.Command, verb string) {
	cmd.Flags().Bool("all", false, fmt.Sprintf("%s all categories", verb))
	for _, spec := range manifest.AllCategories {
		cmd.Flags().Bool(spec.Name, false, fmt.Sprintf("%s %s", verb, spec.Name))
	}
}

// resolveCategoriesFromCmd reads the boolean category flags registered
// by registerCategoryFlags and returns the matching manifest.CategorySet.
// --all sets every spec; an explicit per-category flag sets only that
// spec. No-flag invocations return the zero set; callers (export and
// push, both via applyCategorySelection) detect the zero set and prompt
// via ui.SelectCategories.
func resolveCategoriesFromCmd(cmd *cobra.Command) (manifest.CategorySet, error) {
	all, err := cmd.Flags().GetBool("all")
	if err != nil {
		return manifest.CategorySet{}, fmt.Errorf("read --all flag: %w", err)
	}
	var set manifest.CategorySet
	if all {
		for _, spec := range manifest.AllCategories {
			spec.Apply(&set, true)
		}
		return set, nil
	}
	for _, spec := range manifest.AllCategories {
		v, err := cmd.Flags().GetBool(spec.Name)
		if err != nil {
			return manifest.CategorySet{}, fmt.Errorf("read --%s flag: %w", spec.Name, err)
		}
		spec.Apply(&set, v)
	}
	return set, nil
}
