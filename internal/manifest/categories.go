package manifest

import (
	"errors"
	"fmt"
)

// CategorySet specifies which export categories the user selected.
// AllCategories is the single source of truth for valid categories and wire names.
type CategorySet struct {
	Sessions    bool
	Memory      bool
	History     bool
	FileHistory bool
	Config      bool
	Todos       bool
	UsageData   bool
	PluginsData bool
	Tasks       bool
}

// CategorySpec defines one entry in the export-category enum table: the
// on-wire name in metadata.xml plus Get/Set accessors onto the matching
// CategorySet field.
type CategorySpec struct {
	Name string
	Get  func(*CategorySet) bool
	Set  func(*CategorySet, bool)
}

// AllCategories is the source of truth for the nine export categories.
// Slice order is the canonical display order used by every consumer (CLI
// help, dry-run summaries, metadata.xml entries).
var AllCategories = []CategorySpec{
	{
		Name: "sessions",
		Get:  func(c *CategorySet) bool { return c.Sessions },
		Set:  func(c *CategorySet, v bool) { c.Sessions = v },
	},
	{
		Name: "memory",
		Get:  func(c *CategorySet) bool { return c.Memory },
		Set:  func(c *CategorySet, v bool) { c.Memory = v },
	},
	{
		Name: "history",
		Get:  func(c *CategorySet) bool { return c.History },
		Set:  func(c *CategorySet, v bool) { c.History = v },
	},
	{
		Name: "file-history",
		Get:  func(c *CategorySet) bool { return c.FileHistory },
		Set:  func(c *CategorySet, v bool) { c.FileHistory = v },
	},
	{
		Name: "config",
		Get:  func(c *CategorySet) bool { return c.Config },
		Set:  func(c *CategorySet, v bool) { c.Config = v },
	},
	{
		Name: "todos",
		Get:  func(c *CategorySet) bool { return c.Todos },
		Set:  func(c *CategorySet, v bool) { c.Todos = v },
	},
	{
		Name: "usage-data",
		Get:  func(c *CategorySet) bool { return c.UsageData },
		Set:  func(c *CategorySet, v bool) { c.UsageData = v },
	},
	{
		Name: "plugins-data",
		Get:  func(c *CategorySet) bool { return c.PluginsData },
		Set:  func(c *CategorySet, v bool) { c.PluginsData = v },
	},
	{
		Name: "tasks",
		Get:  func(c *CategorySet) bool { return c.Tasks },
		Set:  func(c *CategorySet, v bool) { c.Tasks = v },
	},
}

// BuildCategoryEntries produces a []Category in AllCategories order for writing
// into metadata.xml. Every AllCategories.Name appears exactly once.
func BuildCategoryEntries(set *CategorySet) []Category {
	entries := make([]Category, len(AllCategories))
	for i, spec := range AllCategories {
		entries[i] = Category{Name: spec.Name, Included: spec.Get(set)}
	}
	return entries
}

// ApplyCategoryEntries validates a manifest's category list and returns the
// matching CategorySet. Missing and unknown names are aggregated via errors.Join.
func ApplyCategoryEntries(entries []Category) (CategorySet, error) {
	specByName := make(map[string]CategorySpec, len(AllCategories))
	for _, spec := range AllCategories {
		specByName[spec.Name] = spec
	}

	var set CategorySet
	seen := make(map[string]bool, len(entries))
	var errs []error

	for _, entry := range entries {
		spec, known := specByName[entry.Name]
		if !known {
			errs = append(errs, fmt.Errorf("unknown manifest category name: %q", entry.Name))
			continue
		}
		seen[entry.Name] = true
		spec.Set(&set, entry.Included)
	}

	for _, spec := range AllCategories {
		if !seen[spec.Name] {
			errs = append(errs, fmt.Errorf("missing manifest category name: %q", spec.Name))
		}
	}

	if len(errs) > 0 {
		return CategorySet{}, errors.Join(errs...)
	}
	return set, nil
}
