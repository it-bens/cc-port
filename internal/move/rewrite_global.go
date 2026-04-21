package move

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// rewriteGlobalFiles rewrites history.jsonl, session files, settings.json, and
// the user config file in place, saving originals to the tracker for rollback.
func rewriteGlobalFiles(
	ctx context.Context,
	claudeHome *claude.Home,
	locations *claude.ProjectLocations,
	moveOptions Options,
	tracker *globalFileTracker,
) error {
	if err := rewriteHistoryFile(ctx, claudeHome, moveOptions, tracker); err != nil {
		return err
	}
	if err := rewriteSessionFiles(ctx, locations, moveOptions, tracker); err != nil {
		return err
	}
	if err := rewriteSettingsFile(ctx, claudeHome, moveOptions, tracker); err != nil {
		return err
	}
	if err := rewriteConfigFile(ctx, claudeHome, moveOptions, tracker); err != nil {
		return err
	}
	for group, path := range locations.AllFlatFiles() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := rewriteTracked(path, moveOptions.OldPath, moveOptions.NewPath, tracker); err != nil {
			return fmt.Errorf("rewrite %s %s: %w", group.Name, path, err)
		}
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
) error {
	historyFile := claudeHome.HistoryFile()
	info, err := os.Stat(historyFile)
	if err != nil {
		return nil
	}

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
		_, _ = fmt.Fprintf(
			resolveWarningWriter(moveOptions),
			"warning: history.jsonl contains %d malformed line(s) at %v: preserved verbatim, not rewritten\n",
			len(malformed), malformed,
		)
	}
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
	_, malformed, err := rewrite.StreamHistoryJSONL(
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

	_, malformed, streamErr := rewrite.StreamHistoryJSONL(
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
) error {
	for _, sessionFilePath := range locations.SessionFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		original, mode, err := tracker.save(sessionFilePath)
		if err != nil {
			return fmt.Errorf("read session file %s for backup: %w", sessionFilePath, err)
		}
		rewritten, _, err := rewrite.SessionFile(original, moveOptions.OldPath, moveOptions.NewPath)
		if err != nil {
			return fmt.Errorf("rewrite session file %s: %w", sessionFilePath, err)
		}
		if err := rewrite.SafeWriteFile(sessionFilePath, rewritten, mode); err != nil {
			return fmt.Errorf("write session file %s: %w", sessionFilePath, err)
		}
	}
	return nil
}

func rewriteSettingsFile(
	ctx context.Context,
	claudeHome *claude.Home,
	moveOptions Options,
	tracker *globalFileTracker,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	settingsFile := claudeHome.SettingsFile()
	if _, err := os.Stat(settingsFile); err != nil {
		return nil
	}
	if err := rewriteTracked(settingsFile, moveOptions.OldPath, moveOptions.NewPath, tracker); err != nil {
		return fmt.Errorf("rewrite settings.json: %w", err)
	}
	return nil
}

func rewriteConfigFile(
	ctx context.Context,
	claudeHome *claude.Home,
	moveOptions Options,
	tracker *globalFileTracker,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	configFile := claudeHome.ConfigFile
	if _, err := os.Stat(configFile); err != nil {
		return nil
	}

	original, mode, err := tracker.save(configFile)
	if err != nil {
		return fmt.Errorf("read config file for backup: %w", err)
	}
	rewritten, _, err := rewrite.UserConfig(original, moveOptions.OldPath, moveOptions.NewPath)
	if err != nil {
		return fmt.Errorf("rewrite config file: %w", err)
	}
	if err := rewrite.SafeWriteFile(configFile, rewritten, mode); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
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
