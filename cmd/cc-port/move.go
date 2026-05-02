package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/move"
)

// findActive is the test seam for lock.FindActive. Swapped in
// movecmd_test.go via withMoveSeams.
var findActive = lock.FindActive

// newMoveCmd returns the move subcommand with closure-scoped flag locals.
// claudeDir points at the persistent root flag's local; cobra populates
// it on flag parse, so the RunE closure must dereference at call time.
func newMoveCmd(claudeDir *string) *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
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

			claudeHome, err := claude.NewHome(*claudeDir)
			if err != nil {
				return err
			}

			if !apply {
				return runMoveDryRun(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), claudeHome, moveOptions)
			}
			return move.Apply(ctx, claudeHome, moveOptions)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "execute the move (default is dry-run)")
	cmd.Flags().Bool(
		"refs-only", false,
		"update references only, do not move project directory on disk",
	)
	cmd.Flags().Bool(
		"rewrite-transcripts", false,
		"also rewrite paths in session transcripts",
	)
	return cmd
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

func runMoveDryRun(
	ctx context.Context,
	stdout, stderr io.Writer,
	claudeHome *claude.Home,
	moveOptions move.Options,
) error {
	movePlan, err := move.DryRun(ctx, claudeHome, moveOptions)
	if err != nil {
		return err
	}

	if err := reportActiveSessionOnSource(stderr, claudeHome, moveOptions.OldPath); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(stdout, "cc-port move (dry-run)")
	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintf(stdout, "  ┌ Directory Rename\n")
	_, _ = fmt.Fprintf(stdout, "  │ %s\n", movePlan.OldProjectDir)
	_, _ = fmt.Fprintf(stdout, "  │ -> %s\n", movePlan.NewProjectDir)
	_, _ = fmt.Fprintln(stdout, "  │")

	renderReferencesBlock(stdout, movePlan)
	_, _ = fmt.Fprintln(stdout, "  │")

	if moveOptions.RewriteTranscripts {
		_, _ = fmt.Fprintf(stdout, "  ├ Transcripts: %d replacements\n", movePlan.TranscriptReplacements)
	} else {
		_, _ = fmt.Fprintf(stdout, "  ├ Transcripts (--rewrite-transcripts not set, skipping)\n")
	}
	_, _ = fmt.Fprintln(stdout, "  │")

	_, _ = fmt.Fprintf(
		stdout,
		"  ├ File-history snapshots: %d preserved verbatim "+
			"(Claude Code reads them by filename for in-session rewinds, not as path references)\n",
		movePlan.ReplacementsByCategory["file-history-snapshots"],
	)
	_, _ = fmt.Fprintln(stdout, "  │")

	renderPlanWarnings(stdout, movePlan)

	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, "  Run with --apply to execute.")
	return nil
}

// reportActiveSessionOnSource prints a heads-up to stderr for every live
// Claude Code session whose recorded cwd equals oldProjectPath. Dry-run
// runs before lock.WithLock would fire on --apply, so surfacing the
// witness here lets an operator close the live session before typing
// --apply instead of discovering the block at mutation time.
func reportActiveSessionOnSource(stderr io.Writer, claudeHome *claude.Home, oldProjectPath string) error {
	active, err := findActive(claudeHome)
	if err != nil {
		return fmt.Errorf("check active sessions: %w", err)
	}
	for _, session := range active {
		if session.Cwd != oldProjectPath {
			continue
		}
		_, _ = fmt.Fprintf(
			stderr,
			"note: Claude Code is currently running on %s (pid %d); --apply will refuse until that session exits\n",
			session.Cwd, session.Pid,
		)
	}
	return nil
}

// displayLabels maps planCategories keys to left-column labels used in the
// dry-run output. Keys absent from this map fall back to the key itself.
var displayLabels = map[string]string{
	"history":                    "history.jsonl",
	"sessions":                   "sessions/*.json",
	"settings":                   "settings.json",
	"plugins/installed_plugins":  "plugins/installed_plugins.json",
	"plugins/known_marketplaces": "plugins/known_marketplaces.json",
	"todos":                      "todos/",
	"usage-data/session-meta":    "usage-data/session-meta/",
	"usage-data/facets":          "usage-data/facets/",
	"plugins-data":               "plugins/data/",
	"tasks":                      "tasks/",
}

func renderReferencesBlock(stdout io.Writer, movePlan *move.Plan) {
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
	_, _ = fmt.Fprintf(stdout, "  ├ References (%d changes)\n", totalChanges)
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
		_, _ = fmt.Fprintf(stdout, "  │   %-32s %d replacements\n", label, count)
	}
	if movePlan.ConfigBlockRekey {
		_, _ = fmt.Fprintf(stdout, "  │   %-32s re-key project block\n", "~/.claude.json")
	}
}

func renderPlanWarnings(stdout io.Writer, movePlan *move.Plan) {
	if len(movePlan.HistoryMalformedLines) > 0 {
		_, _ = fmt.Fprintf(
			stdout,
			"  ├ Warning: history.jsonl has %d malformed line(s) at %v: preserved verbatim, not rewritten\n",
			len(movePlan.HistoryMalformedLines), movePlan.HistoryMalformedLines,
		)
		_, _ = fmt.Fprintln(stdout, "  │")
	}

	if len(movePlan.RulesWarnings) > 0 {
		_, _ = fmt.Fprintf(stdout, "  └ Warning: Rules files with matching paths:\n")
		for _, warning := range movePlan.RulesWarnings {
			_, _ = fmt.Fprintf(stdout, "      %s (line %d)\n", warning.File, warning.Line)
		}
	} else {
		_, _ = fmt.Fprintf(stdout, "  └ No rules file warnings\n")
	}
}
