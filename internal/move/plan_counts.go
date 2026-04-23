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
func scanHistoryFile(ctx context.Context, claudeHome *claude.Home, moveOptions Options) (count int, malformed []int, err error) {
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

	lineNumber := 0
	for scanner.Scan() {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, nil, ctxErr
		}
		lineNumber++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var probe claude.HistoryEntry
		if jsonErr := json.Unmarshal(line, &probe); jsonErr != nil {
			malformed = append(malformed, lineNumber)
			continue
		}
		_, replaced := rewrite.ReplacePathInBytes(line, moveOptions.OldPath, moveOptions.NewPath)
		if replaced > 0 {
			count++
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return 0, nil, fmt.Errorf("scan history.jsonl: %w", scanErr)
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
			return 0, fmt.Errorf("analyze session file %s: %w", sessionFilePath, err)
		}
		if changed {
			count++
		}
	}
	return count, nil
}

// countUserWideReplacements walks claude.UserWideRewriteTargets, counts
// boundary-aware occurrences of moveOptions.OldPath per target, and writes
// each count to plan.ReplacementsByCategory[target.Name]. Missing targets
// contribute zero, matching the existing convention for absent global files.
func countUserWideReplacements(
	ctx context.Context,
	plan *Plan,
	claudeHome *claude.Home,
	moveOptions Options,
) error {
	for _, target := range claude.UserWideRewriteTargets {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := target.Path(claudeHome)
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat %s: %w", path, err)
		}
		data, err := os.ReadFile(path) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		_, count := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
		plan.ReplacementsByCategory[target.Name] = count
	}
	return nil
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
		return false, fmt.Errorf("analyze config file: %w", err)
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
