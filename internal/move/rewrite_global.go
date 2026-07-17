package move

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// rewriteGlobalFiles rewrites history.jsonl, session files, settings.json, and
// the user config file in place, saving originals to the tracker for rollback.
func rewriteGlobalFiles(
	ctx context.Context,
	claudeHome *claude.Home,
	locations *claude.ProjectLocations,
	moveOptions Options,
	tracker *globalFileTracker,
	phase progress.PhaseHandle,
) error {
	if err := rewriteHistoryFile(ctx, claudeHome, moveOptions, tracker, phase); err != nil {
		return err
	}
	if err := rewriteSessionFiles(ctx, locations, moveOptions, tracker, phase); err != nil {
		return err
	}
	if err := rewriteUserWideFiles(ctx, claudeHome, moveOptions, tracker, phase); err != nil {
		return err
	}
	if err := rewriteConfigFile(ctx, claudeHome, moveOptions, tracker, phase); err != nil {
		return err
	}
	return rewriteSessionKeyedFiles(ctx, locations, moveOptions, tracker, phase)
}

// rewriteSessionKeyedFiles rewrites every session-keyed flat file, opening one
// sub-phase per claude.SessionKeyedGroups entry so a group with zero present
// files still reports a (total=0) sub-phase. Files are collected once through
// locations.AllFlatFiles (which applies each group's sidecar filter), then the
// registry is walked in canonical order.
func rewriteSessionKeyedFiles(
	ctx context.Context,
	locations *claude.ProjectLocations,
	moveOptions Options,
	tracker *globalFileTracker,
	phase progress.PhaseHandle,
) error {
	collected := make(map[string][]string)
	for group, path := range locations.AllFlatFiles() {
		collected[group.Name] = append(collected[group.Name], path)
	}

	for group := range claude.SessionKeyedGroups() {
		paths := collected[group.Name]
		groupPhase := phase.SubPhase(group.Name, int64(len(paths)), progress.UnitFiles)
		for _, path := range paths {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := rewriteTrackedPreservingMtime(path, moveOptions.OldPath, moveOptions.NewPath, tracker); err != nil {
				return fmt.Errorf("rewrite %s %s: %w", group.Name, path, err)
			}
			groupPhase.Advance(1)
		}
		groupPhase.End(fmt.Sprintf("%d files", len(paths)))
	}
	return nil
}

// rewriteHistoryFile rewrites history.jsonl via StreamHistoryJSONL.
// The rollback snapshot route is chosen by file size: for files smaller
// than siblingBackupThreshold the tracker holds the bytes in memory; for
// larger files the tracker copies the original to a sibling rollback file
// first so restore can rename it back without paging the whole file into
// RAM. cleanupSiblings removes any sibling on success.
func rewriteHistoryFile(
	ctx context.Context,
	claudeHome *claude.Home,
	moveOptions Options,
	tracker *globalFileTracker,
	phase progress.PhaseHandle,
) error {
	historyFile := claudeHome.HistoryFile()
	info, err := os.Stat(historyFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", historyFile, err)
	}

	historyPhase := phase.SubPhase("history", 1, progress.UnitFiles)

	var malformed []int
	if info.Size() < siblingBackupThreshold {
		malformed, err = inMemoryHistoryRewrite(ctx, historyFile, moveOptions, tracker)
	} else {
		malformed, err = siblingHistoryRewrite(ctx, historyFile, moveOptions, tracker)
	}
	if err != nil {
		return err
	}

	if len(malformed) > 0 {
		moveOptions.Reporter.Warn(fmt.Errorf(
			"history.jsonl contains %d malformed line(s) at %v: preserved verbatim, not rewritten",
			len(malformed), malformed,
		))
	}

	historyPhase.Advance(1)
	historyPhase.End("history.jsonl")
	return nil
}

// inMemoryHistoryRewrite reads historyFile whole, saves the original
// bytes to the tracker's in-memory snapshot, rewrites through
// StreamHistoryJSONL, and writes the result back via SafeWriteFile.
// Chosen for files under siblingBackupThreshold where paging the backup
// through disk buys nothing.
func inMemoryHistoryRewrite(
	ctx context.Context,
	historyFile string,
	moveOptions Options,
	tracker *globalFileTracker,
) ([]int, error) {
	original, mode, err := tracker.save(historyFile)
	if err != nil {
		return nil, fmt.Errorf("back up history.jsonl: %w", err)
	}

	var rewritten bytes.Buffer
	_, malformed, err := claude.StreamHistoryJSONL(
		ctx, bytes.NewReader(original), &rewritten, moveOptions.OldPath, moveOptions.NewPath,
	)
	if err != nil {
		return nil, fmt.Errorf("rewrite history.jsonl: %w", err)
	}
	if err := rewrite.SafeWriteFile(historyFile, rewritten.Bytes(), mode); err != nil {
		return nil, fmt.Errorf("write history.jsonl: %w", err)
	}
	return malformed, nil
}

// siblingHistoryRewrite copies historyFile to a sibling rollback file via
// the tracker, streams it through StreamHistoryJSONL, and atomically
// promotes the result onto historyFile. The original is never held in
// memory, so peak RAM stays bounded regardless of history size.
func siblingHistoryRewrite(
	ctx context.Context,
	historyFile string,
	moveOptions Options,
	tracker *globalFileTracker,
) ([]int, error) {
	backupPath, mode, err := tracker.saveToSibling(historyFile)
	if err != nil {
		return nil, fmt.Errorf("back up history.jsonl: %w", err)
	}

	source, err := os.Open(backupPath) //nolint:gosec // G304: path constructed from trusted internal data
	if err != nil {
		return nil, fmt.Errorf("open history backup: %w", err)
	}
	defer func() { _ = source.Close() }()

	tempPath := historyFile + ".cc-port-rewrite.tmp"
	//nolint:gosec // G304: path constructed from trusted internal data
	destination, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return nil, fmt.Errorf("create history rewrite temp: %w", err)
	}

	_, malformed, streamErr := claude.StreamHistoryJSONL(
		ctx, source, destination, moveOptions.OldPath, moveOptions.NewPath,
	)
	if streamErr != nil {
		_ = destination.Close()
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("rewrite history.jsonl: %w", streamErr)
	}
	if err := destination.Close(); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("close history rewrite temp: %w", err)
	}
	if err := os.Rename(tempPath, historyFile); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("promote history rewrite: %w", err)
	}
	return malformed, nil
}

func rewriteSessionFiles(
	ctx context.Context,
	locations *claude.ProjectLocations,
	moveOptions Options,
	tracker *globalFileTracker,
	phase progress.PhaseHandle,
) error {
	sessionsPhase := phase.SubPhase("sessions", int64(len(locations.SessionFiles)), progress.UnitFiles)
	for _, sessionFilePath := range locations.SessionFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		original, mode, err := tracker.save(sessionFilePath)
		if err != nil {
			return fmt.Errorf("read session file %s for backup: %w", sessionFilePath, err)
		}
		rewritten, _, err := claude.RewriteSessionFile(original, moveOptions.OldPath, moveOptions.NewPath)
		if err != nil {
			return fmt.Errorf("rewrite session file %s: %w", sessionFilePath, err)
		}
		if err := rewrite.SafeWriteFile(sessionFilePath, rewritten, mode); err != nil {
			return fmt.Errorf("write session file %s: %w", sessionFilePath, err)
		}
		sessionsPhase.Advance(1)
	}
	sessionsPhase.End(fmt.Sprintf("%d files", len(locations.SessionFiles)))
	return nil
}

// rewriteUserWideFiles applies boundary-aware byte replacement to every file
// registered in claude.UserWideRewriteTargets. Each target is stat-gated so
// absent files skip (matching the existing settings-missing behavior) and
// non-ErrNotExist stat errors abort Apply so rollback runs. Every rewrite
// flows through rewriteTracked so the globalFileTracker can restore
// byte-for-byte on a later failure.
func rewriteUserWideFiles(
	ctx context.Context,
	claudeHome *claude.Home,
	moveOptions Options,
	tracker *globalFileTracker,
	phase progress.PhaseHandle,
) error {
	for target := range claude.UserWideRewriteTargets() {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := target.RewritePath(claudeHome)
		present := true
		if _, err := os.Stat(path); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("stat %s: %w", path, err)
			}
			present = false
		}

		targetPhase := phase.SubPhase(target.Name, boolToInt64(present), progress.UnitFiles)
		if !present {
			targetPhase.End("absent")
			continue
		}
		if err := rewriteTracked(path, moveOptions.OldPath, moveOptions.NewPath, tracker); err != nil {
			return fmt.Errorf("rewrite %s %s: %w", target.Name, path, err)
		}
		targetPhase.Advance(1)
		targetPhase.End(target.Name)
	}
	return nil
}

// boolToInt64 maps a presence flag to a phase total: 1 for a present file, 0
// for an absent one, so an absent user-wide target still opens a zero-total
// sub-phase and the user-wide registry stays fully covered.
func boolToInt64(present bool) int64 {
	if present {
		return 1
	}
	return 0
}

func rewriteConfigFile(
	ctx context.Context,
	claudeHome *claude.Home,
	moveOptions Options,
	tracker *globalFileTracker,
	phase progress.PhaseHandle,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	configFile := claudeHome.ConfigFile
	if _, err := os.Stat(configFile); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", configFile, err)
	}

	configPhase := phase.SubPhase("config", 1, progress.UnitFiles)

	original, mode, err := tracker.save(configFile)
	if err != nil {
		return fmt.Errorf("read config file for backup: %w", err)
	}
	rewritten, _, err := claude.RewriteUserConfig(original, moveOptions.OldPath, moveOptions.NewPath)
	if err != nil {
		return fmt.Errorf("rewrite config file: %w", err)
	}
	if err := rewrite.SafeWriteFile(configFile, rewritten, mode); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	configPhase.Advance(1)
	configPhase.End("~/.claude.json")
	return nil
}

// rewriteTracked performs the save → byte-replace → atomic-write sandwich used
// by every uniform plain-bytes rewrite. The tracker snapshots the original
// bytes and mode so a later failure can restore them; errors propagate
// verbatim so callers can wrap with per-group context.
func rewriteTracked(path, oldPath, newPath string, tracker *globalFileTracker) error {
	original, mode, err := tracker.save(path)
	if err != nil {
		return err
	}
	rewritten, _ := rewrite.ReplacePathInBytes(original, oldPath, newPath)
	return rewrite.SafeWriteFile(path, rewritten, mode)
}

// rewriteTrackedPreservingMtime is rewriteTracked plus restoration of the
// file's pre-rewrite modification time, for the session-keyed flat files whose
// mtime a move must preserve.
func rewriteTrackedPreservingMtime(path, oldPath, newPath string, tracker *globalFileTracker) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if err := rewriteTracked(path, oldPath, newPath, tracker); err != nil {
		return err
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("restore mtime %s: %w", path, err)
	}
	return nil
}
