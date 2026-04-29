package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
)

func TestAllCategories_CanonicalOrder(t *testing.T) {
	expected := []string{
		"sessions", "memory", "history", "file-history", "config",
		"todos", "usage-data", "plugins-data", "tasks",
	}
	require.Len(t, manifest.AllCategories, len(expected))
	for i, want := range expected {
		assert.Equal(t, want, manifest.AllCategories[i].Name, "position %d", i)
	}
}

func TestBuildCategoryEntries_LengthAndOrder(t *testing.T) {
	set := manifest.CategorySet{Sessions: true, UsageData: true}
	entries := manifest.BuildCategoryEntries(&set)

	require.Len(t, entries, 9)
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	assert.Equal(t, []string{
		"sessions", "memory", "history", "file-history", "config",
		"todos", "usage-data", "plugins-data", "tasks",
	}, names)
}

func TestBuildCategoryEntries_IncludedReflectsGet(t *testing.T) {
	set := manifest.CategorySet{
		Sessions: true, Memory: false, History: true, FileHistory: false, Config: true,
		Todos: false, UsageData: true, PluginsData: false, Tasks: true,
	}
	entries := manifest.BuildCategoryEntries(&set)

	want := map[string]bool{
		"sessions": true, "memory": false, "history": true, "file-history": false, "config": true,
		"todos": false, "usage-data": true, "plugins-data": false, "tasks": true,
	}
	for _, e := range entries {
		assert.Equal(t, want[e.Name], e.Included, "entry %s", e.Name)
	}
}

func TestApplyCategoryEntries_RoundTripsCategorySet(t *testing.T) {
	original := manifest.CategorySet{
		Sessions: true, Memory: false, History: true, FileHistory: true, Config: false,
		Todos: true, UsageData: false, PluginsData: true, Tasks: false,
	}
	entries := manifest.BuildCategoryEntries(&original)

	got, err := manifest.ApplyCategoryEntries(entries)
	require.NoError(t, err)
	assert.Equal(t, original, got)
}

func TestApplyCategoryEntries_MissingNameIsError(t *testing.T) {
	entries := manifest.BuildCategoryEntries(&manifest.CategorySet{})
	// Drop the "tasks" entry.
	truncated := entries[:len(entries)-1]

	_, err := manifest.ApplyCategoryEntries(truncated)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tasks")
}

func TestApplyCategoryEntries_EveryMissingNameListed(t *testing.T) {
	// Drop the last two entries (plugins-data + tasks).
	entries := manifest.BuildCategoryEntries(&manifest.CategorySet{})[:7]

	_, err := manifest.ApplyCategoryEntries(entries)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plugins-data")
	assert.Contains(t, err.Error(), "tasks")
}

func TestApplyCategoryEntries_UnknownNameIsError(t *testing.T) {
	entries := manifest.BuildCategoryEntries(&manifest.CategorySet{})
	entries = append(entries, manifest.Category{Name: "bogus", Included: true})

	_, err := manifest.ApplyCategoryEntries(entries)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
}

func TestApplyCategoryEntries_MissingAndUnknownAggregated(t *testing.T) {
	// Drop "tasks", add "bogus".
	entries := manifest.BuildCategoryEntries(&manifest.CategorySet{})[:8]
	entries = append(entries, manifest.Category{Name: "bogus", Included: true})

	_, err := manifest.ApplyCategoryEntries(entries)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tasks")
	assert.Contains(t, err.Error(), "bogus")
}

func TestBuildApplyBuild_IsStable(t *testing.T) {
	original := manifest.CategorySet{Sessions: true, FileHistory: true, Tasks: true}
	entries1 := manifest.BuildCategoryEntries(&original)
	applied, err := manifest.ApplyCategoryEntries(entries1)
	require.NoError(t, err)
	entries2 := manifest.BuildCategoryEntries(&applied)
	assert.Equal(t, entries1, entries2)
}

func TestSpecByName_ReturnsKnownCategory(t *testing.T) {
	spec, ok := manifest.SpecByName("sessions")
	require.True(t, ok)
	assert.Equal(t, "sessions", spec.Name)
}

func TestSpecByName_RejectsUnknownCategory(t *testing.T) {
	_, ok := manifest.SpecByName("does-not-exist")
	assert.False(t, ok)
}
