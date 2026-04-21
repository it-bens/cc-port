package move

import (
	"fmt"
	"os"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/rewrite"
)

func scanHistoryFile(claudeHome *claude.Home, moveOptions Options) (int, []int, error) {
	historyFile := claudeHome.HistoryFile()
	if _, err := os.Stat(historyFile); err != nil {
		return 0, nil, nil
	}

	data, err := os.ReadFile(historyFile) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return 0, nil, fmt.Errorf("read history.jsonl: %w", err)
	}
	_, count, malformed, err := rewrite.HistoryJSONL(data, moveOptions.OldPath, moveOptions.NewPath)
	if err != nil {
		return 0, nil, fmt.Errorf("analyse history.jsonl: %w", err)
	}
	return count, malformed, nil
}

func countSessionFileReplacements(locations *claude.ProjectLocations, moveOptions Options) (int, error) {
	count := 0
	for _, sessionFilePath := range locations.SessionFiles {
		data, err := os.ReadFile(sessionFilePath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return 0, fmt.Errorf("read session file %s: %w", sessionFilePath, err)
		}
		_, changed, err := rewrite.SessionFile(data, moveOptions.OldPath, moveOptions.NewPath)
		if err != nil {
			return 0, fmt.Errorf("analyse session file %s: %w", sessionFilePath, err)
		}
		if changed {
			count++
		}
	}
	return count, nil
}

func countSettingsReplacements(claudeHome *claude.Home, moveOptions Options) (int, error) {
	settingsFile := claudeHome.SettingsFile()
	if _, err := os.Stat(settingsFile); err != nil {
		return 0, nil
	}

	data, err := os.ReadFile(settingsFile) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return 0, fmt.Errorf("read settings.json: %w", err)
	}
	_, count := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
	return count, nil
}

func checkConfigBlockRekey(claudeHome *claude.Home, moveOptions Options) (bool, error) {
	if _, err := os.Stat(claudeHome.ConfigFile); err != nil {
		return false, nil
	}

	data, err := os.ReadFile(claudeHome.ConfigFile)
	if err != nil {
		return false, fmt.Errorf("read config file: %w", err)
	}
	_, rekeyed, err := rewrite.UserConfig(data, moveOptions.OldPath, moveOptions.NewPath)
	if err != nil {
		return false, fmt.Errorf("analyse config file: %w", err)
	}
	return rekeyed, nil
}

func countTranscriptReplacements(locations *claude.ProjectLocations, moveOptions Options) (int, error) {
	transcriptPaths, err := listTranscriptFiles(locations.ProjectDir)
	if err != nil {
		return 0, err
	}

	total := 0
	for _, transcriptPath := range transcriptPaths {
		data, err := os.ReadFile(transcriptPath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return 0, fmt.Errorf("read transcript %s: %w", transcriptPath, err)
		}
		_, count := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
		total += count
	}
	return total, nil
}

// countFileHistorySnapshots returns the number of snapshot files under the
// project's file-history directories for the dry-run plan.
func countFileHistorySnapshots(locations *claude.ProjectLocations) (int, error) {
	paths, err := snapshotPaths(locations)
	if err != nil {
		return 0, err
	}
	return len(paths), nil
}

// countSessionKeyedReplacements writes per-group replacement counts into
// plan.ReplacementsByCategory by walking locations.AllFlatFiles() exactly
// once. A file with any non-zero replacement count counts once toward its
// group total.
func countSessionKeyedReplacements(plan *Plan, locations *claude.ProjectLocations, moveOptions Options) error {
	for group, path := range locations.AllFlatFiles() {
		data, err := os.ReadFile(path) //nolint:gosec // path from trusted ProjectLocations
		if err != nil {
			return fmt.Errorf("read %s file %s: %w", group.Name, path, err)
		}
		_, n := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
		if n > 0 {
			plan.ReplacementsByCategory[group.Name]++
		}
	}
	return nil
}
