// Package move implements project directory move operations for Claude Code projects.
package move

import (
	"context"
	"fmt"
	"io"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/scan"
)

// Options holds the parameters for a project move operation.
type Options struct {
	OldPath            string
	NewPath            string
	RewriteTranscripts bool
	RefsOnly           bool

	// WarningWriter receives human-readable warnings emitted during Apply
	// (e.g. malformed lines in history.jsonl that the move preserves but
	// cannot repair). Defaults to os.Stderr when nil. DryRun does not use
	// this field — it surfaces warnings through Plan instead.
	WarningWriter io.Writer
}

// Plan holds the results of a dry-run move operation.
type Plan struct {
	OldProjectDir string
	NewProjectDir string

	// ReplacementsByCategory carries per-category replacement counts keyed
	// on the canonical planCategories names. Missing keys read as zero.
	ReplacementsByCategory map[string]int

	ConfigBlockRekey       bool
	TranscriptReplacements int

	HistoryMalformedLines []int
	RulesWarnings         []scan.Warning
	MoveProjectDir        bool
}

// planCategories is the canonical ordering of ReplacementsByCategory keys:
// history, sessions, settings, then the five claude.SessionKeyedGroups in
// order, then file-history-snapshots as a tail counter.
var planCategories = func() []string {
	out := []string{"history", "sessions", "settings"}
	for _, group := range claude.SessionKeyedGroups {
		out = append(out, group.Name)
	}
	out = append(out, "file-history-snapshots")
	return out
}()

// PlanCategories returns the canonical ordering of ReplacementsByCategory
// keys so CLI renderers iterate in a stable order.
func PlanCategories() []string {
	out := make([]string, len(planCategories))
	copy(out, planCategories)
	return out
}

// DryRun computes the move plan without writing any files; lock-free contrast to Apply.
func DryRun(ctx context.Context, claudeHome *claude.Home, moveOptions Options) (*Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("canceled: %w", err)
	}
	if err := checkEncodedDirCollision(claudeHome, moveOptions.OldPath, moveOptions.NewPath); err != nil {
		return nil, err
	}

	locations, err := claude.LocateProject(claudeHome, moveOptions.OldPath)
	if err != nil {
		return nil, fmt.Errorf("locate project: %w", err)
	}

	plan := &Plan{
		OldProjectDir:          claudeHome.ProjectDir(moveOptions.OldPath),
		NewProjectDir:          claudeHome.ProjectDir(moveOptions.NewPath),
		MoveProjectDir:         !moveOptions.RefsOnly,
		ReplacementsByCategory: make(map[string]int, len(planCategories)),
	}

	if err := populatePlanCounts(ctx, plan, claudeHome, locations, moveOptions); err != nil {
		return nil, err
	}

	warnings, err := scan.Rules(claudeHome.RulesDir(), moveOptions.OldPath)
	if err != nil {
		return nil, fmt.Errorf("scan rules: %w", err)
	}
	plan.RulesWarnings = warnings

	return plan, nil
}

// populatePlanCounts fills every counter on plan by walking each count helper
// in ReplacementsByCategory order; keeps DryRun's top-level flow readable.
func populatePlanCounts(
	ctx context.Context,
	plan *Plan,
	claudeHome *claude.Home,
	locations *claude.ProjectLocations,
	moveOptions Options,
) error {
	historyCount, malformed, err := scanHistoryFile(ctx, claudeHome, moveOptions)
	if err != nil {
		return err
	}
	plan.ReplacementsByCategory["history"] = historyCount
	plan.HistoryMalformedLines = malformed

	sessionCount, err := countSessionFileReplacements(ctx, locations, moveOptions)
	if err != nil {
		return err
	}
	plan.ReplacementsByCategory["sessions"] = sessionCount

	settingsCount, err := countSettingsReplacements(ctx, claudeHome, moveOptions)
	if err != nil {
		return err
	}
	plan.ReplacementsByCategory["settings"] = settingsCount

	plan.ConfigBlockRekey, err = checkConfigBlockRekey(ctx, claudeHome, moveOptions)
	if err != nil {
		return err
	}

	if moveOptions.RewriteTranscripts {
		plan.TranscriptReplacements, err = countTranscriptReplacements(ctx, locations, moveOptions)
		if err != nil {
			return err
		}
	}

	snapshots, err := countFileHistorySnapshots(ctx, locations)
	if err != nil {
		return err
	}
	plan.ReplacementsByCategory["file-history-snapshots"] = snapshots

	return countSessionKeyedReplacements(ctx, plan, locations, moveOptions)
}

// Apply performs the project move via copy-verify-delete inside lock.WithLock.
func Apply(ctx context.Context, claudeHome *claude.Home, moveOptions Options) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("canceled: %w", err)
	}
	return lock.WithLock(claudeHome, func() error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("canceled: %w", err)
		}
		if err := checkEncodedDirCollision(claudeHome, moveOptions.OldPath, moveOptions.NewPath); err != nil {
			return err
		}

		locations, err := claude.LocateProject(claudeHome, moveOptions.OldPath)
		if err != nil {
			return fmt.Errorf("locate project: %w", err)
		}

		oldProjectDir := claudeHome.ProjectDir(moveOptions.OldPath)
		newProjectDir := claudeHome.ProjectDir(moveOptions.NewPath)
		return executeMove(ctx, claudeHome, locations, oldProjectDir, newProjectDir, moveOptions)
	})
}
