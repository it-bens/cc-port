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
	var selectedCategories []string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Select categories to export").
				Options(
					huh.NewOption("Sessions (transcripts & subagent data)", "sessions").Selected(true),
					huh.NewOption("Memory (project-scoped auto-memory)", "memory").Selected(true),
					huh.NewOption("History (command history entries)", "history").Selected(true),
					huh.NewOption("File history (file version snapshots)", "file-history"),
					huh.NewOption("Config (project config from ~/.claude.json)", "config").Selected(true),
					huh.NewOption("Todos (in-progress TodoWrite task lists)", "todos"),
					huh.NewOption("Usage data (session metadata + token facets)", "usage-data"),
					huh.NewOption("Plugin data (per-session plugin state)", "plugins-data"),
					huh.NewOption("Tasks (numbered agent-task lists)", "tasks"),
				).
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
	specByName := make(map[string]manifest.CategorySpec, len(manifest.AllCategories))
	for _, spec := range manifest.AllCategories {
		specByName[spec.Name] = spec
	}
	var result manifest.CategorySet
	for _, key := range selections {
		spec, ok := specByName[key]
		if !ok {
			return manifest.CategorySet{}, fmt.Errorf("unknown export category key %q", key)
		}
		spec.Apply(&result, true)
	}
	return result, nil
}
