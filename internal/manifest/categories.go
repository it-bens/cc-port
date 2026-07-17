package manifest

import (
	"errors"
	"fmt"
	"strings"
)

// UnknownCategoriesError reports manifest category names, within one tool's
// block, that the tool's registry does not declare. Names carries the
// offending names in encounter order; callers inspect it via errors.As.
// Returned by ApplyToolCategories.
type UnknownCategoriesError struct {
	Tool  string
	Names []string
}

func (e *UnknownCategoriesError) Error() string {
	return fmt.Sprintf("unknown category name(s) for tool %q: %s", e.Tool, strings.Join(e.Names, ", "))
}

// MissingCategoriesError reports category names a tool's registry declares
// that are absent from that tool's manifest block. Names carries the
// missing names in registry order; callers inspect it via errors.As.
// Returned by ApplyToolCategories.
type MissingCategoriesError struct {
	Tool  string
	Names []string
}

func (e *MissingCategoriesError) Error() string {
	return fmt.Sprintf("missing category name(s) for tool %q: %s", e.Tool, strings.Join(e.Names, ", "))
}

// DuplicateCategoriesError reports category names that occur more than once
// in one manifest tool block.
type DuplicateCategoriesError struct {
	Tool  string
	Names []string
}

func (e *DuplicateCategoriesError) Error() string {
	return fmt.Sprintf("duplicate category name(s) for tool %q: %s", e.Tool, strings.Join(e.Names, ", "))
}

// UnregisteredToolError reports a manifest <tool name=...> block whose name
// does not match any tool in the caller's registry. Import fails hard on
// this rather than skipping it: an unregistered name signals an archive
// built by a newer or foreign cc-port, not merely a tool absent on this
// machine.
type UnregisteredToolError struct {
	Tool string
}

func (e *UnregisteredToolError) Error() string {
	return fmt.Sprintf("manifest declares unregistered tool %q", e.Tool)
}

// BuildToolCategoryEntries produces a []Category in declaredNames order for
// writing into one tool's manifest block. declaredNames is that tool's
// Categories() names in registration order; every name appears exactly once.
func BuildToolCategoryEntries(declaredNames []string, selected map[string]bool) []Category {
	entries := make([]Category, len(declaredNames))
	for i, name := range declaredNames {
		entries[i] = Category{Name: name, Included: selected[name]}
	}
	return entries
}

// ApplyToolCategories validates one tool's manifest category entries against
// declaredNames (that tool's registered category names, in registration
// order) and returns the selection as name -> included. Every declaredNames
// entry must appear exactly once in entries; an entries.Name absent from
// declaredNames is unknown.
func ApplyToolCategories(toolName string, declaredNames []string, entries []Category) (map[string]bool, error) {
	declared := make(map[string]struct{}, len(declaredNames))
	for _, name := range declaredNames {
		declared[name] = struct{}{}
	}

	selected := make(map[string]bool, len(entries))
	seen := make(map[string]bool, len(entries))
	var unknown, missing, duplicates []string

	for _, entry := range entries {
		if _, ok := declared[entry.Name]; !ok {
			unknown = append(unknown, entry.Name)
			continue
		}
		if seen[entry.Name] {
			duplicates = append(duplicates, entry.Name)
			continue
		}
		seen[entry.Name] = true
		selected[entry.Name] = entry.Included
	}
	for _, name := range declaredNames {
		if !seen[name] {
			missing = append(missing, name)
		}
	}

	var errs []error
	if len(unknown) > 0 {
		errs = append(errs, &UnknownCategoriesError{Tool: toolName, Names: unknown})
	}
	if len(missing) > 0 {
		errs = append(errs, &MissingCategoriesError{Tool: toolName, Names: missing})
	}
	if len(duplicates) > 0 {
		errs = append(errs, &DuplicateCategoriesError{Tool: toolName, Names: duplicates})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return selected, nil
}
