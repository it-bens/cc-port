// Package ui provides interactive terminal prompts for the cc-port CLI.
package ui

import (
	"fmt"

	"charm.land/huh/v2"

	"github.com/it-bens/cc-port/internal/export"
)

// SelectCategories presents an interactive multi-select for export categories.
func SelectCategories() (export.CategorySet, error) {
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
				).
				Value(&selectedCategories),
		),
	)

	if err := form.Run(); err != nil {
		return export.CategorySet{}, fmt.Errorf("category selection cancelled: %w", err)
	}

	var categories export.CategorySet
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
		}
	}
	return categories, nil
}

// ResolvePlaceholder prompts the user to resolve a single placeholder.
func ResolvePlaceholder(key, original, autoValue string) (string, error) {
	resolvedValue := autoValue

	title := fmt.Sprintf("Resolve %s", key)
	description := fmt.Sprintf("Original: %s", original)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(title).
				Description(description).
				Placeholder("Enter path or press Enter to skip").
				Value(&resolvedValue),
		),
	)

	if err := form.Run(); err != nil {
		return "", fmt.Errorf("resolution cancelled: %w", err)
	}

	return resolvedValue, nil
}

// ConfirmApply asks the user to confirm the move operation.
func ConfirmApply(description string) (bool, error) {
	var confirmed bool

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Apply changes?").
				Description(description).
				Value(&confirmed),
		),
	)

	if err := form.Run(); err != nil {
		return false, err
	}

	return confirmed, nil
}
