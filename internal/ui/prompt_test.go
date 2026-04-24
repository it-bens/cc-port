package ui

import (
	"errors"
	"io"
	"sync"
	"testing"

	"charm.land/huh/v2"
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

func TestSelectCategoriesRejectsNonInteractiveStdin(t *testing.T) {
	withSeams(t, seamOverrides{
		isTerminalFunc: func(uintptr) bool { return false },
	})

	_, err := SelectCategories()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--all")
	assert.Contains(t, err.Error(), "--sessions")
}

func TestResolvePlaceholderRejectsNonInteractiveStdin(t *testing.T) {
	withSeams(t, seamOverrides{
		isTerminalFunc: func(uintptr) bool { return false },
	})

	_, err := ResolvePlaceholder("PROJECT_PATH", "/old", "/new")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "PROJECT_PATH")
	assert.Contains(t, err.Error(), "export manifest")
}

func TestSelectCategoriesWrapsFormError(t *testing.T) {
	sentinel := errors.New("test form failure")
	withSeams(t, seamOverrides{
		isTerminalFunc: func(uintptr) bool { return true },
		runFormFunc:    func(*huh.Form) error { return sentinel },
		banner:         io.Discard,
		bannerOnce:     &sync.Once{},
	})

	_, err := SelectCategories()

	require.Error(t, err)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "category selection canceled")
}

func TestResolvePlaceholderWrapsFormError(t *testing.T) {
	sentinel := errors.New("test placeholder failure")
	withSeams(t, seamOverrides{
		isTerminalFunc: func(uintptr) bool { return true },
		runFormFunc:    func(*huh.Form) error { return sentinel },
		banner:         io.Discard,
		bannerOnce:     &sync.Once{},
	})

	_, err := ResolvePlaceholder("PROJECT_PATH", "/old", "/new")

	require.Error(t, err)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "resolution canceled")
}

// seamOverrides carries nil-or-value replacements for the package-level test
// seams. A nil / zero field means "leave the seam alone." withSeams always
// restores every known seam on cleanup regardless of which were overridden.
type seamOverrides struct {
	isTerminalFunc func(uintptr) bool
	runFormFunc    func(*huh.Form) error
	banner         io.Writer
	bannerOnce     *sync.Once
}

func withSeams(t *testing.T, opts seamOverrides) {
	t.Helper()
	origIsTerminal := isTerminal
	origRunForm := runForm
	origBannerWriter := bannerWriter
	origBannerOnce := interactiveBannerOnce
	t.Cleanup(func() {
		isTerminal = origIsTerminal
		runForm = origRunForm
		bannerWriter = origBannerWriter
		interactiveBannerOnce = origBannerOnce
	})
	if opts.isTerminalFunc != nil {
		isTerminal = opts.isTerminalFunc
	}
	if opts.runFormFunc != nil {
		runForm = opts.runFormFunc
	}
	if opts.banner != nil {
		bannerWriter = opts.banner
	}
	if opts.bannerOnce != nil {
		interactiveBannerOnce = opts.bannerOnce
	}
}
