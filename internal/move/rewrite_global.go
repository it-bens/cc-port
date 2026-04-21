package move

import (
	"fmt"
	"os"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// rewriteGlobalFiles rewrites history.jsonl, session files, settings.json, and
// the user config file in place, saving originals to the tracker for rollback.
func rewriteGlobalFiles(
	claudeHome *claude.Home,
	locations *claude.ProjectLocations,
	moveOptions Options,
	tracker *globalFileTracker,
) error {
	if err := rewriteHistoryFile(claudeHome, moveOptions, tracker); err != nil {
		return err
	}
	if err := rewriteSessionFiles(locations, moveOptions, tracker); err != nil {
		return err
	}
	if err := rewriteSettingsFile(claudeHome, moveOptions, tracker); err != nil {
		return err
	}
	if err := rewriteConfigFile(claudeHome, moveOptions, tracker); err != nil {
		return err
	}
	for group, path := range locations.AllFlatFiles() {
		if err := rewriteTracked(path, moveOptions.OldPath, moveOptions.NewPath, tracker); err != nil {
			return fmt.Errorf("rewrite %s %s: %w", group.Name, path, err)
		}
	}
	return nil
}

func rewriteHistoryFile(claudeHome *claude.Home, moveOptions Options, tracker *globalFileTracker) error {
	historyFile := claudeHome.HistoryFile()
	if _, err := os.Stat(historyFile); err != nil {
		return nil
	}

	original, mode, err := tracker.save(historyFile)
	if err != nil {
		return fmt.Errorf("read history.jsonl for backup: %w", err)
	}
	rewritten, _, malformed, err := rewrite.HistoryJSONL(original, moveOptions.OldPath, moveOptions.NewPath)
	if err != nil {
		return fmt.Errorf("rewrite history.jsonl: %w", err)
	}
	if err := rewrite.SafeWriteFile(historyFile, rewritten, mode); err != nil {
		return fmt.Errorf("write history.jsonl: %w", err)
	}
	if len(malformed) > 0 {
		_, _ = fmt.Fprintf(
			resolveWarningWriter(moveOptions),
			"warning: history.jsonl contains %d malformed line(s) at %v — preserved verbatim, not rewritten\n",
			len(malformed), malformed,
		)
	}
	return nil
}

func rewriteSessionFiles(
	locations *claude.ProjectLocations,
	moveOptions Options,
	tracker *globalFileTracker,
) error {
	for _, sessionFilePath := range locations.SessionFiles {
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

func rewriteSettingsFile(claudeHome *claude.Home, moveOptions Options, tracker *globalFileTracker) error {
	settingsFile := claudeHome.SettingsFile()
	if _, err := os.Stat(settingsFile); err != nil {
		return nil
	}
	if err := rewriteTracked(settingsFile, moveOptions.OldPath, moveOptions.NewPath, tracker); err != nil {
		return fmt.Errorf("rewrite settings.json: %w", err)
	}
	return nil
}

func rewriteConfigFile(claudeHome *claude.Home, moveOptions Options, tracker *globalFileTracker) error {
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
