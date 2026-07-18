package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
)

var testDeclaredNames = []string{"sessions", "memory", "history"}

func TestBuildToolCategoryEntries_LengthAndOrder(t *testing.T) {
	selected := map[string]bool{"sessions": true, "history": true}
	entries := manifest.BuildToolCategoryEntries(testDeclaredNames, selected)

	require.Len(t, entries, 3)
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	assert.Equal(t, testDeclaredNames, names)
}

func TestBuildToolCategoryEntries_IncludedReflectsSelection(t *testing.T) {
	selected := map[string]bool{"sessions": true, "memory": false, "history": true}
	entries := manifest.BuildToolCategoryEntries(testDeclaredNames, selected)

	want := map[string]bool{"sessions": true, "memory": false, "history": true}
	for _, e := range entries {
		assert.Equal(t, want[e.Name], e.Included, "entry %s", e.Name)
	}
}

func TestAbsentToolBlockReturnsFreshEmptySelectionAndNoPlaceholders(t *testing.T) {
	firstSelection, firstPlaceholders := manifest.AbsentToolBlock()
	assert.Empty(t, firstSelection)
	assert.Nil(t, firstPlaceholders)
	firstSelection["sessions"] = true

	secondSelection, secondPlaceholders := manifest.AbsentToolBlock()

	assert.Empty(t, secondSelection)
	assert.Nil(t, secondPlaceholders)
}

func TestApplyToolCategories_RoundTripsSelection(t *testing.T) {
	original := map[string]bool{"sessions": true, "memory": false, "history": true}
	entries := manifest.BuildToolCategoryEntries(testDeclaredNames, original)

	got, err := manifest.ApplyToolCategories("claude", testDeclaredNames, entries)
	require.NoError(t, err)
	assert.Equal(t, original, got)
}

func TestApplyToolCategories_MissingNameIsError(t *testing.T) {
	entries := manifest.BuildToolCategoryEntries(testDeclaredNames, nil)
	truncated := entries[:len(entries)-1] // drop "history"

	_, err := manifest.ApplyToolCategories("claude", testDeclaredNames, truncated)

	var missingErr *manifest.MissingCategoriesError
	require.ErrorAs(t, err, &missingErr)
	assert.Equal(t, "claude", missingErr.Tool)
	assert.Contains(t, missingErr.Names, "history")
}

func TestApplyToolCategories_UnknownNameIsError(t *testing.T) {
	entries := manifest.BuildToolCategoryEntries(testDeclaredNames, nil)
	entries = append(entries, manifest.Category{Name: "bogus", Included: true})

	_, err := manifest.ApplyToolCategories("claude", testDeclaredNames, entries)

	var unknownErr *manifest.UnknownCategoriesError
	require.ErrorAs(t, err, &unknownErr)
	assert.Equal(t, "claude", unknownErr.Tool)
	assert.Contains(t, unknownErr.Names, "bogus")
}

func TestApplyToolCategories_DuplicateNameIsError(t *testing.T) {
	entries := manifest.BuildToolCategoryEntries(testDeclaredNames, map[string]bool{"sessions": true})
	entries = append(entries, manifest.Category{Name: "sessions", Included: false})

	_, err := manifest.ApplyToolCategories("claude", testDeclaredNames, entries)

	var duplicateErr *manifest.DuplicateCategoriesError
	require.ErrorAs(t, err, &duplicateErr)
	assert.Equal(t, "claude", duplicateErr.Tool)
	assert.Equal(t, []string{"sessions"}, duplicateErr.Names)
}

func TestApplyToolCategories_MissingAndUnknownAggregated(t *testing.T) {
	entries := manifest.BuildToolCategoryEntries(testDeclaredNames, nil)[:2] // drop "history"
	entries = append(entries, manifest.Category{Name: "bogus", Included: true})

	_, err := manifest.ApplyToolCategories("claude", testDeclaredNames, entries)

	var missingErr *manifest.MissingCategoriesError
	require.ErrorAs(t, err, &missingErr)
	assert.Contains(t, missingErr.Names, "history")

	var unknownErr *manifest.UnknownCategoriesError
	require.ErrorAs(t, err, &unknownErr)
	assert.Contains(t, unknownErr.Names, "bogus")
}

func TestBuildApplyBuild_IsStable(t *testing.T) {
	original := map[string]bool{"sessions": true, "history": true}
	entries1 := manifest.BuildToolCategoryEntries(testDeclaredNames, original)
	applied, err := manifest.ApplyToolCategories("claude", testDeclaredNames, entries1)
	require.NoError(t, err)
	entries2 := manifest.BuildToolCategoryEntries(testDeclaredNames, applied)
	assert.Equal(t, entries1, entries2)
}

func TestUnregisteredToolError_Message(t *testing.T) {
	err := &manifest.UnregisteredToolError{Tool: "codex"}
	assert.Contains(t, err.Error(), "codex")
}
