package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/move"
)

var (
	moveApply              bool
	moveRefsOnly           bool
	moveRewriteTranscripts bool
)

var moveCmd = &cobra.Command{
	Use:   "move <old-path> <new-path>",
	Short: "Move a project and update Claude Code references",
	Long: "Renames a project directory and rewrites all Claude Code references.\n" +
		"Default is dry-run — use --apply to execute.",
	Args: cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		claudeHome, err := claude.NewHome(claudeDir)
		if err != nil {
			return err
		}

		oldPath, err := claude.ResolveProjectPath(args[0])
		if err != nil {
			return fmt.Errorf("resolve old path: %w", err)
		}
		newPath, err := claude.ResolveProjectPath(args[1])
		if err != nil {
			return fmt.Errorf("resolve new path: %w", err)
		}

		moveOptions := move.Options{
			OldPath:            oldPath,
			NewPath:            newPath,
			RefsOnly:           moveRefsOnly,
			RewriteTranscripts: moveRewriteTranscripts,
		}

		if !moveApply {
			return runMoveDryRun(claudeHome, moveOptions)
		}
		return move.Apply(claudeHome, moveOptions)
	},
}

func init() {
	moveCmd.Flags().BoolVar(&moveApply, "apply", false, "execute the move (default is dry-run)")
	moveCmd.Flags().BoolVar(
		&moveRefsOnly, "refs-only", false,
		"update references only, do not move project directory on disk",
	)
	moveCmd.Flags().BoolVar(
		&moveRewriteTranscripts, "rewrite-transcripts", false,
		"also rewrite paths in session transcripts",
	)
	rootCmd.AddCommand(moveCmd)
}

func runMoveDryRun(claudeHome *claude.Home, moveOptions move.Options) error {
	movePlan, err := move.DryRun(claudeHome, moveOptions)
	if err != nil {
		return err
	}

	fmt.Println("cc-port move (dry-run)")
	fmt.Println()
	fmt.Printf("  ┌ Directory Rename\n")
	fmt.Printf("  │ %s\n", movePlan.OldProjectDir)
	fmt.Printf("  │ -> %s\n", movePlan.NewProjectDir)
	fmt.Println("  │")

	renderReferencesBlock(movePlan)
	fmt.Println("  │")

	if moveOptions.RewriteTranscripts {
		fmt.Printf("  ├ Transcripts: %d replacements\n", movePlan.TranscriptReplacements)
	} else {
		fmt.Printf("  ├ Transcripts (--rewrite-transcripts not set, skipping)\n")
	}
	fmt.Println("  │")

	fmt.Printf(
		"  ├ File-history snapshots: %d preserved (contents not rewritten — used for in-session rewinds)\n",
		movePlan.ReplacementsByCategory["file-history-snapshots"],
	)
	fmt.Println("  │")

	renderPlanWarnings(movePlan)

	fmt.Println()
	fmt.Println("  Run with --apply to execute.")
	return nil
}

// displayLabels maps planCategories keys to left-column labels used in the
// dry-run output. Keys absent from this map fall back to the key itself.
var displayLabels = map[string]string{
	"history":                 "history.jsonl",
	"sessions":                "sessions/*.json",
	"settings":                "settings.json",
	"todos":                   "todos/",
	"usage-data/session-meta": "usage-data/session-meta/",
	"usage-data/facets":       "usage-data/facets/",
	"plugins-data":            "plugins/data/",
	"tasks":                   "tasks/",
}

func renderReferencesBlock(movePlan *move.Plan) {
	totalChanges := 0
	for _, key := range move.PlanCategories() {
		if key == "file-history-snapshots" {
			continue
		}
		totalChanges += movePlan.ReplacementsByCategory[key]
	}
	if movePlan.ConfigBlockRekey {
		totalChanges++
	}
	fmt.Printf("  ├ References (%d changes)\n", totalChanges)
	for _, key := range move.PlanCategories() {
		if key == "file-history-snapshots" {
			continue
		}
		count := movePlan.ReplacementsByCategory[key]
		if count == 0 {
			continue
		}
		label, ok := displayLabels[key]
		if !ok {
			label = key
		}
		fmt.Printf("  │   %-26s %d replacements\n", label, count)
	}
	if movePlan.ConfigBlockRekey {
		fmt.Printf("  │   %-26s re-key project block\n", "~/.claude.json")
	}
}

func renderPlanWarnings(movePlan *move.Plan) {
	if len(movePlan.HistoryMalformedLines) > 0 {
		fmt.Printf(
			"  ├ Warning: history.jsonl has %d malformed line(s) at %v — preserved verbatim, not rewritten\n",
			len(movePlan.HistoryMalformedLines), movePlan.HistoryMalformedLines,
		)
		fmt.Println("  │")
	}

	if len(movePlan.RulesWarnings) > 0 {
		fmt.Printf("  └ Warning: Rules files with matching paths:\n")
		for _, warning := range movePlan.RulesWarnings {
			fmt.Printf("      %s (line %d)\n", warning.File, warning.Line)
		}
	} else {
		fmt.Printf("  └ No rules file warnings\n")
	}
}
