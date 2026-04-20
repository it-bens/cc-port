// Package ui provides interactive terminal prompts for the cc-port CLI.
package ui

import (
	"fmt"
	"os"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"

	"github.com/it-bens/cc-port/internal/manifest"
)

// requireTTY fails fast when stdin is not a terminal, with a message naming
// the non-interactive alternative for the calling surface. huh's own error in
// that situation is an opaque "open /dev/tty" failure after the form has
// already taken over the terminal.
func requireTTY(remediation string) error {
	if term.IsTerminal(os.Stdin.Fd()) {
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

	if err := form.Run(); err != nil {
		return manifest.CategorySet{}, fmt.Errorf("category selection cancelled: %w", err)
	}

	var categories manifest.CategorySet
	for _, selection := range selectedCategories {
		switch selection {
		case "sessions":
			categories.Sessions = true
		case "memory":
			categories.Memory = true
		case "history":
			categories.History = true
		case "file-history":
			categories.FileHistory = true
		case "config":
			categories.Config = true
		case "todos":
			categories.Todos = true
		case "usage-data":
			categories.UsageData = true
		case "plugins-data":
			categories.PluginsData = true
		case "tasks":
			categories.Tasks = true
		}
	}
	return categories, nil
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

	if err := form.Run(); err != nil {
		return "", fmt.Errorf("resolution cancelled: %w", err)
	}

	return resolvedValue, nil
}
