// Package move implements project directory move operations for Claude Code projects.
package move

import (
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

// DryRun analyses what a move would change without writing any files.
// It locates all project data, counts replacements for each file type,
// and scans rules files for warnings.
func DryRun(claudeHome *claude.Home, moveOptions Options) (*Plan, error) {
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

	historyCount, malformed, err := scanHistoryFile(claudeHome, moveOptions)
	if err != nil {
		return nil, err
	}
	plan.ReplacementsByCategory["history"] = historyCount
	plan.HistoryMalformedLines = malformed

	if plan.ReplacementsByCategory["sessions"], err = countSessionFileReplacements(locations, moveOptions); err != nil {
		return nil, err
	}

	if plan.ReplacementsByCategory["settings"], err = countSettingsReplacements(claudeHome, moveOptions); err != nil {
		return nil, err
	}

	plan.ConfigBlockRekey, err = checkConfigBlockRekey(claudeHome, moveOptions)
	if err != nil {
		return nil, err
	}

	if moveOptions.RewriteTranscripts {
		plan.TranscriptReplacements, err = countTranscriptReplacements(locations, moveOptions)
		if err != nil {
			return nil, err
		}
	}

	snapshots, err := countFileHistorySnapshots(locations)
	if err != nil {
		return nil, err
	}
	plan.ReplacementsByCategory["file-history-snapshots"] = snapshots

	if err := countSessionKeyedReplacements(plan, locations, moveOptions); err != nil {
		return nil, err
	}

	warnings, err := scan.Rules(claudeHome.RulesDir(), moveOptions.OldPath)
	if err != nil {
		return nil, fmt.Errorf("scan rules: %w", err)
	}
	plan.RulesWarnings = warnings

	return plan, nil
}

// Apply performs the project move. It uses a copy-verify-delete strategy so
// that originals are only removed after all new data is successfully created.
//
// On any failure, all newly created paths are removed and the originals
// remain untouched.
//
// Apply wraps its body in lock.WithLock, which acquires the advisory lock
// over claudeHome and aborts if a Claude Code session is currently live or
// if another cc-port invocation is already operating on the same directory.
func Apply(claudeHome *claude.Home, moveOptions Options) error {
	return lock.WithLock(claudeHome, func() error {
		if err := checkEncodedDirCollision(claudeHome, moveOptions.OldPath, moveOptions.NewPath); err != nil {
			return err
		}

		locations, err := claude.LocateProject(claudeHome, moveOptions.OldPath)
		if err != nil {
			return fmt.Errorf("locate project: %w", err)
		}

		oldProjectDir := claudeHome.ProjectDir(moveOptions.OldPath)
		newProjectDir := claudeHome.ProjectDir(moveOptions.NewPath)
		return executeMove(claudeHome, locations, oldProjectDir, newProjectDir, moveOptions)
	})
}
