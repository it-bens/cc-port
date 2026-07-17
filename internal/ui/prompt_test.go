package ui

import (
	"errors"
	"io"
	"sync"
	"testing"

	"charm.land/huh/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

type countingBanner struct{ calls int }

func (c *countingBanner) Render(io.Writer) error {
	c.calls++
	return nil
}

func TestShowInteractiveBannerCallsRenderOncePerProcess(t *testing.T) {
	withSeams(t, seamOverrides{
		bannerOnce: &sync.Once{},
	})
	banner := &countingBanner{}

	showInteractiveBanner(banner)
	showInteractiveBanner(banner)
	showInteractiveBanner(banner)

	assert.Equal(t, 1, banner.calls, "banner.Render must be called exactly once across multiple showInteractiveBanner invocations")
}

func TestSelectCategoriesRejectsNonInteractiveStdin(t *testing.T) {
	withSeams(t, seamOverrides{
		isTerminalFunc: func(uintptr) bool { return false },
	})

	_, err := SelectCategories(&countingBanner{}, []tool.Tool{claude.New()})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--all")
	assert.Contains(t, err.Error(), "--include")
}

func TestSelectCategoriesWrapsFormError(t *testing.T) {
	sentinel := errors.New("test form failure")
	withSeams(t, seamOverrides{
		isTerminalFunc: func(uintptr) bool { return true },
		runFormFunc:    func(*huh.Form) error { return sentinel },
		bannerOnce:     &sync.Once{},
	})

	_, err := SelectCategories(&countingBanner{}, []tool.Tool{claude.New()})

	require.Error(t, err)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "category selection canceled")
}

// seamOverrides carries nil-or-value replacements for the package-level test
// seams. A nil / zero field means "leave the seam alone." withSeams always
// restores every known seam on cleanup regardless of which were overridden.
type seamOverrides struct {
	isTerminalFunc func(uintptr) bool
	runFormFunc    func(*huh.Form) error
	bannerOnce     *sync.Once
}

func withSeams(t *testing.T, opts seamOverrides) {
	t.Helper()
	origIsTerminal := isTerminal
	origRunForm := runForm
	origBannerOnce := interactiveBannerOnce
	t.Cleanup(func() {
		isTerminal = origIsTerminal
		runForm = origRunForm
		interactiveBannerOnce = origBannerOnce
	})
	if opts.isTerminalFunc != nil {
		isTerminal = opts.isTerminalFunc
	}
	if opts.runFormFunc != nil {
		runForm = opts.runFormFunc
	}
	if opts.bannerOnce != nil {
		interactiveBannerOnce = opts.bannerOnce
	}
}
