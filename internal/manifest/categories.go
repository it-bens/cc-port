package manifest

import (
	"errors"
	"fmt"
	"strings"
)

// UnknownCategoriesError reports manifest category names that are not in
// AllCategories. Names carries the offending names in encounter order; callers
// inspect it via errors.As. Returned by ApplyCategoryEntries.
type UnknownCategoriesError struct {
	Names []string
}

func (e *UnknownCategoriesError) Error() string {
	return fmt.Sprintf("unknown manifest category name(s): %s", strings.Join(e.Names, ", "))
}

// MissingCategoriesError reports AllCategories names absent from the manifest's
// category list. Names carries the missing names in AllCategories order; callers
// inspect it via errors.As. Returned by ApplyCategoryEntries.
type MissingCategoriesError struct {
	Names []string
}

func (e *MissingCategoriesError) Error() string {
	return fmt.Sprintf("missing manifest category name(s): %s", strings.Join(e.Names, ", "))
}

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
// on-wire name in metadata.xml plus function-typed Value/Apply hooks onto
// the matching CategorySet field. These are fields, not interface methods,
// so Go's "no Get prefix" convention is sidestepped by naming them for
// what they do rather than what a Java bean would call them.
type CategorySpec struct {
	Name  string
	Value func(*CategorySet) bool
	Apply func(*CategorySet, bool)
}

// AllCategories is the source of truth for the export categories.
// Slice order is the canonical display order used by every consumer (CLI
// help, dry-run summaries, metadata.xml entries).
var AllCategories = []CategorySpec{
	{
		Name:  "sessions",
		Value: func(c *CategorySet) bool { return c.Sessions },
		Apply: func(c *CategorySet, v bool) { c.Sessions = v },
	},
	{
		Name:  "memory",
		Value: func(c *CategorySet) bool { return c.Memory },
		Apply: func(c *CategorySet, v bool) { c.Memory = v },
	},
	{
		Name:  "history",
		Value: func(c *CategorySet) bool { return c.History },
		Apply: func(c *CategorySet, v bool) { c.History = v },
	},
	{
		Name:  "file-history",
		Value: func(c *CategorySet) bool { return c.FileHistory },
		Apply: func(c *CategorySet, v bool) { c.FileHistory = v },
	},
	{
		Name:  "config",
		Value: func(c *CategorySet) bool { return c.Config },
		Apply: func(c *CategorySet, v bool) { c.Config = v },
	},
	{
		Name:  "todos",
		Value: func(c *CategorySet) bool { return c.Todos },
		Apply: func(c *CategorySet, v bool) { c.Todos = v },
	},
	{
		Name:  "usage-data",
		Value: func(c *CategorySet) bool { return c.UsageData },
		Apply: func(c *CategorySet, v bool) { c.UsageData = v },
	},
	{
		Name:  "plugins-data",
		Value: func(c *CategorySet) bool { return c.PluginsData },
		Apply: func(c *CategorySet, v bool) { c.PluginsData = v },
	},
	{
		Name:  "tasks",
		Value: func(c *CategorySet) bool { return c.Tasks },
		Apply: func(c *CategorySet, v bool) { c.Tasks = v },
	},
}

// SpecByName returns the CategorySpec for name, or ok=false when name is not in AllCategories.
func SpecByName(name string) (CategorySpec, bool) {
	for _, spec := range AllCategories {
		if spec.Name == name {
			return spec, true
		}
	}
	return CategorySpec{}, false
}

// BuildCategoryEntries produces a []Category in AllCategories order for writing
// into metadata.xml. Every AllCategories.Name appears exactly once.
func BuildCategoryEntries(set *CategorySet) []Category {
	entries := make([]Category, len(AllCategories))
	for i, spec := range AllCategories {
		entries[i] = Category{Name: spec.Name, Included: spec.Value(set)}
	}
	return entries
}

// ApplyCategoryEntries validates a manifest's category list and returns the
// matching CategorySet. Unknown and missing names are collected into an
// UnknownCategoriesError and a MissingCategoriesError, joined via errors.Join.
func ApplyCategoryEntries(entries []Category) (CategorySet, error) {
	var set CategorySet
	seen := make(map[string]bool, len(entries))
	var unknown, missing []string

	for _, entry := range entries {
		spec, known := SpecByName(entry.Name)
		if !known {
			unknown = append(unknown, entry.Name)
			continue
		}
		seen[entry.Name] = true
		spec.Apply(&set, entry.Included)
	}

	for _, spec := range AllCategories {
		if !seen[spec.Name] {
			missing = append(missing, spec.Name)
		}
	}

	var errs []error
	if len(unknown) > 0 {
		errs = append(errs, &UnknownCategoriesError{Names: unknown})
	}
	if len(missing) > 0 {
		errs = append(errs, &MissingCategoriesError{Names: missing})
	}
	if len(errs) > 0 {
		return CategorySet{}, errors.Join(errs...)
	}
	return set, nil
}
