// Package ui provides interactive terminal prompts for the cc-port CLI.
package ui

import (
	"fmt"
	"io"
	"os"
	"sync"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"

	"github.com/it-bens/cc-port/internal/logo"
	"github.com/it-bens/cc-port/internal/manifest"
)

// Interactive flows can call into each other (export prompts categories
// then may prompt placeholders); sync.Once keeps the banner to a single
// render per process. Addressable so withSeams can re-point it to a fresh
// &sync.Once{} per test; value-typed Once cannot be reassigned after use
// without tripping copylocks.
var interactiveBannerOnce = &sync.Once{}

// Test seams. Production behavior is unchanged; every seam defaults to the
// real dependency it replaces.
var (
	isTerminal             = term.IsTerminal
	runForm                = (*huh.Form).Run
	bannerWriter io.Writer = os.Stdout
)

func showInteractiveBanner() {
	interactiveBannerOnce.Do(func() {
		_ = logo.Render(bannerWriter)
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

// optionMeta carries UI-specific metadata for one export-category option.
// Keyed by manifest.AllCategories.Name in categoryOptionMeta.
type optionMeta struct {
	Description string
	Selected    bool
}

var categoryOptionMeta = map[string]optionMeta{
	"sessions":     {Description: "Sessions (transcripts & subagent data)", Selected: true},
	"memory":       {Description: "Memory (project-scoped auto-memory)", Selected: true},
	"history":      {Description: "History (command history entries)", Selected: true},
	"file-history": {Description: "File history (file version snapshots)"},
	"config":       {Description: "Config (project config from ~/.claude.json)", Selected: true},
	"todos":        {Description: "Todos (in-progress TodoWrite task lists)"},
	"usage-data":   {Description: "Usage data (session metadata + token facets)"},
	"plugins-data": {Description: "Plugin data (per-session plugin state)"},
	"tasks":        {Description: "Tasks (numbered agent-task lists)"},
}

// SelectCategories presents an interactive multi-select for export categories.
func SelectCategories() (manifest.CategorySet, error) {
	if err := requireTTY(
		"rerun with --all or explicit category flags " +
			"(--sessions, --memory, --history, --file-history, --config, " +
			"--todos, --usage-data, --plugins-data, --tasks)",
	); err != nil {
		return manifest.CategorySet{}, err
	}
	showInteractiveBanner()

	options := make([]huh.Option[string], 0, len(manifest.AllCategories))
	for _, spec := range manifest.AllCategories {
		meta, ok := categoryOptionMeta[spec.Name]
		if !ok {
			return manifest.CategorySet{}, fmt.Errorf("category %q has no UI option metadata", spec.Name)
		}
		opt := huh.NewOption(meta.Description, spec.Name)
		if meta.Selected {
			opt = opt.Selected(true)
		}
		options = append(options, opt)
	}

	var selectedCategories []string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Select categories to export").
				Options(options...).
				Value(&selectedCategories),
		),
	)

	if err := runForm(form); err != nil {
		return manifest.CategorySet{}, fmt.Errorf("category selection canceled: %w", err)
	}

	result, err := categoriesFromSelections(selectedCategories)
	if err != nil {
		return manifest.CategorySet{}, fmt.Errorf("category selection: %w", err)
	}
	return result, nil
}

// ResolvePlaceholder prompts for one manifest placeholder; returned value is verbatim with no validation.
func ResolvePlaceholder(key, original, autoValue string) (string, error) {
	if err := requireTTY(
		fmt.Sprintf(
			"cannot prompt for placeholder %s; "+
				"use the two-step manifest flow "+
				"(export manifest / import --from-manifest) to supply resolutions non-interactively",
			key,
		),
	); err != nil {
		return "", err
	}
	showInteractiveBanner()

	resolvedValue := autoValue

	title := fmt.Sprintf("Resolve %s", key)
	description := fmt.Sprintf("Original: %s", original)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(title).
				Description(description).
				Placeholder("Enter absolute path").
				Value(&resolvedValue),
		),
	)

	if err := runForm(form); err != nil {
		return "", fmt.Errorf("resolution canceled: %w", err)
	}

	return resolvedValue, nil
}

// An unknown key means the form options literal in SelectCategories has
// drifted out of sync with manifest.AllCategories; surface it rather than
// silently dropping.
func categoriesFromSelections(selections []string) (manifest.CategorySet, error) {
	var result manifest.CategorySet
	for _, key := range selections {
		spec, ok := manifest.SpecByName(key)
		if !ok {
			return manifest.CategorySet{}, fmt.Errorf("unknown export category key %q", key)
		}
		spec.Apply(&result, true)
	}
	return result, nil
}
