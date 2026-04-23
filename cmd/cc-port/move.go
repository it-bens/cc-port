package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/move"
)

var moveApply bool

var moveCmd = &cobra.Command{
	Use:   "move <old-path> <new-path>",
	Short: "Move a project and update Claude Code references",
	Long: "Renames a project directory and rewrites all Claude Code references.\n" +
		"Default is dry-run — use --apply to execute.",
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(2)(cmd, args); err != nil {
			return &usageError{err: err}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		moveOptions, err := parseMoveOptions(cmd, args)
		if err != nil {
			return err
		}

		claudeHome, err := claude.NewHome(claudeDir)
		if err != nil {
			return err
		}

		if !moveApply {
			return runMoveDryRun(ctx, claudeHome, moveOptions)
		}
		return move.Apply(ctx, claudeHome, moveOptions)
	},
}

// parseMoveOptions turns the cobra command + positional args into a
// move.Options. Kept pure (no lock, no domain call) so it is
// unit-testable in isolation.
func parseMoveOptions(cmd *cobra.Command, args []string) (move.Options, error) {
	oldPath, err := claude.ResolveProjectPath(args[0])
	if err != nil {
		return move.Options{}, fmt.Errorf("resolve old path: %w", err)
	}
	newPath, err := claude.ResolveProjectPath(args[1])
	if err != nil {
		return move.Options{}, fmt.Errorf("resolve new path: %w", err)
	}
	if oldPath == newPath {
		return move.Options{}, fmt.Errorf("old and new paths are identical after resolution")
	}
	refsOnly, _ := cmd.Flags().GetBool("refs-only")
	rewriteTranscripts, _ := cmd.Flags().GetBool("rewrite-transcripts")
	return move.Options{
		OldPath:            oldPath,
		NewPath:            newPath,
		RefsOnly:           refsOnly,
		RewriteTranscripts: rewriteTranscripts,
	}, nil
}

func init() {
	moveCmd.Flags().BoolVar(&moveApply, "apply", false, "execute the move (default is dry-run)")
	moveCmd.Flags().Bool(
		"refs-only", false,
		"update references only, do not move project directory on disk",
	)
	moveCmd.Flags().Bool(
		"rewrite-transcripts", false,
		"also rewrite paths in session transcripts",
	)
	rootCmd.AddCommand(moveCmd)
}

func runMoveDryRun(ctx context.Context, claudeHome *claude.Home, moveOptions move.Options) error {
	movePlan, err := move.DryRun(ctx, claudeHome, moveOptions)
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
		"  ├ File-history snapshots: %d preserved verbatim "+
			"(Claude Code reads them by filename for in-session rewinds, not as path references)\n",
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
			"  ├ Warning: history.jsonl has %d malformed line(s) at %v: preserved verbatim, not rewritten\n",
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
