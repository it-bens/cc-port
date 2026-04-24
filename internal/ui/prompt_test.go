package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
)

func TestCategoriesFromSelections(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		got, err := categoriesFromSelections(nil)
		require.NoError(t, err)
		assert.Equal(t, manifest.CategorySet{}, got)
	})

	t.Run("isolates unselected keys", func(t *testing.T) {
		representative := manifest.AllCategories[0]
		got, err := categoriesFromSelections([]string{representative.Name})
		require.NoError(t, err)
		assert.True(t, representative.Value(&got), "selected key %q must be true", representative.Name)
		for _, other := range manifest.AllCategories[1:] {
			assert.False(t, other.Value(&got), "unselected key %q must remain false", other.Name)
		}
	})

	t.Run("all keys", func(t *testing.T) {
		keys := make([]string, len(manifest.AllCategories))
		var want manifest.CategorySet
		for i, spec := range manifest.AllCategories {
			keys[i] = spec.Name
			spec.Apply(&want, true)
		}
		got, err := categoriesFromSelections(keys)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("unknown key", func(t *testing.T) {
		_, err := categoriesFromSelections([]string{"does-not-exist"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does-not-exist")
	})
}
