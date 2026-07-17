// Package ui provides interactive terminal prompts for the cc-port CLI.
package ui

import (
	"fmt"
	"io"
	"os"
	"sync"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"

	"github.com/it-bens/cc-port/internal/tool"
)

// Banner is the consumer-defined interface for the interactive-prompt
// banner. The single Render method matches what showInteractiveBanner
// needs; the implementation chooses whether to draw the gantry-crane
// logo (cc-port-with-logo build) or no-op (default cc-port build).
type Banner interface {
	Render(io.Writer) error
}

// Interactive flows can call into each other (export prompts categories
// then may prompt placeholders); sync.Once keeps the banner to a single
// render per process. Addressable so withSeams can re-point it to a fresh
// &sync.Once{} per test; value-typed Once cannot be reassigned after use
// without tripping copylocks.
var interactiveBannerOnce = &sync.Once{}

// Test seams. Production behavior is unchanged; every seam defaults to
// the real dependency it replaces.
var (
	isTerminal = term.IsTerminal
	runForm    = (*huh.Form).Run
)

func showInteractiveBanner(banner Banner) {
	interactiveBannerOnce.Do(func() {
		// Cosmetic banner write: failure is unrecoverable (no retry,
		// no caller signal that wants it), so swallow the error here
		// rather than propagate through every prompt entry point.
		_ = banner.Render(os.Stdout)
	})
}

// requireTTY fails fast when stdin is not a terminal, with a message naming
// the non-interactive alternative for the calling surface. huh's own error in
// that situation is an opaque "open /dev/tty" failure after the form has
// already taken over the terminal.
func requireTTY(remediation string) error {
	if isTerminal(os.Stdin.Fd()) {
		return nil
	}
	return fmt.Errorf("interactive prompt requires a TTY: %s", remediation)
}

func categoryFlagsHelpText() string {
	return "rerun with --all or --include <tool>/<category>"
}

// SelectCategories presents an interactive multi-select for export
// categories, grouped by tool, across every tool in tools. Returns the
// selection as tool name -> category name -> included.
func SelectCategories(banner Banner, tools []tool.Tool) (map[string]map[string]bool, error) {
	if err := requireTTY(categoryFlagsHelpText()); err != nil {
		return nil, err
	}
	showInteractiveBanner(banner)

	type qualifiedOption struct {
		toolName     string
		categoryName string
	}

	var options []huh.Option[qualifiedOption]
	for _, t := range tools {
		for _, category := range t.Categories() {
			label := category.Description
			if len(tools) > 1 {
				label = fmt.Sprintf("[%s] %s", t.DisplayName(), label)
			}
			opt := huh.NewOption(label, qualifiedOption{toolName: t.Name(), categoryName: category.Name})
			if category.DefaultSelected {
				opt = opt.Selected(true)
			}
			options = append(options, opt)
		}
	}

	var selectedOptions []qualifiedOption
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[qualifiedOption]().
				Title("Select categories to export").
				Options(options...).
				Value(&selectedOptions),
		),
	)

	if err := runForm(form); err != nil {
		return nil, fmt.Errorf("category selection canceled: %w", err)
	}

	selection := make(map[string]map[string]bool)
	for _, t := range tools {
		selection[t.Name()] = make(map[string]bool)
	}
	for _, option := range selectedOptions {
		selection[option.toolName][option.categoryName] = true
	}
	return selection, nil
}
