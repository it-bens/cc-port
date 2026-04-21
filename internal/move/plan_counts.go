package move

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// scanHistoryFile opens history.jsonl and walks it one line at a time,
// counting well-formed lines that would pick up at least one replacement if
// apply ran. Mirrors StreamHistoryJSONL's classification rules so dry-run
// and apply agree on which lines are in scope.
func scanHistoryFile(ctx context.Context, claudeHome *claude.Home, moveOptions Options) (int, []int, error) {
	historyFile := claudeHome.HistoryFile()
	file, err := os.Open(historyFile) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("open history.jsonl: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), claude.MaxHistoryLine)

	count := 0
	lineNumber := 0
	var malformed []int
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		lineNumber++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var probe claude.HistoryEntry
		if err := json.Unmarshal(line, &probe); err != nil {
			malformed = append(malformed, lineNumber)
			continue
		}
		_, replaced := rewrite.ReplacePathInBytes(line, moveOptions.OldPath, moveOptions.NewPath)
		if replaced > 0 {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, nil, fmt.Errorf("scan history.jsonl: %w", err)
	}
	return count, malformed, nil
}

func countSessionFileReplacements(
	ctx context.Context,
	locations *claude.ProjectLocations,
	moveOptions Options,
) (int, error) {
	count := 0
	for _, sessionFilePath := range locations.SessionFiles {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
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

func countSettingsReplacements(ctx context.Context, claudeHome *claude.Home, moveOptions Options) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
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

func checkConfigBlockRekey(ctx context.Context, claudeHome *claude.Home, moveOptions Options) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
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

func countTranscriptReplacements(
	ctx context.Context,
	locations *claude.ProjectLocations,
	moveOptions Options,
) (int, error) {
	transcriptPaths, err := listTranscriptFiles(ctx, locations.ProjectDir)
	if err != nil {
		return 0, err
	}

	total := 0
	for _, transcriptPath := range transcriptPaths {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
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
func countFileHistorySnapshots(ctx context.Context, locations *claude.ProjectLocations) (int, error) {
	paths, err := snapshotPaths(ctx, locations)
	if err != nil {
		return 0, err
	}
	return len(paths), nil
}

// countSessionKeyedReplacements writes per-group replacement counts into
// plan.ReplacementsByCategory by walking locations.AllFlatFiles() exactly
// once. Each file contributes its per-occurrence replacement count so the
// totals match the CLI's "N replacements" label.
func countSessionKeyedReplacements(
	ctx context.Context,
	plan *Plan,
	locations *claude.ProjectLocations,
	moveOptions Options,
) error {
	for group, path := range locations.AllFlatFiles() {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // path from trusted ProjectLocations
		if err != nil {
			return fmt.Errorf("read %s file %s: %w", group.Name, path, err)
		}
		_, n := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
		plan.ReplacementsByCategory[group.Name] += n
	}
	return nil
}
