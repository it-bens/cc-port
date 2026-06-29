package stats

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

// referenceSurfaces is the canonical order of reference surfaces: the global
// history and config files, the project-local transcripts and memory, the
// session-bound sessions/*.json, then the user-wide and session-keyed
// registries. Derived from the registries the way move's plan categories are,
// never a hard-coded list, so a new registry entry shows up automatically.
// file-history is absent on purpose: snapshot bytes are opaque and never
// scanned for references.
func referenceSurfaces() []string {
	surfaces := []string{"history", "sessions", "transcripts", "memory", "config"}
	for _, target := range claude.UserWideRewriteTargets {
		surfaces = append(surfaces, target.Name)
	}
	for _, group := range claude.SessionKeyedGroups {
		surfaces = append(surfaces, group.Name)
	}
	return surfaces
}

// countReferences tallies bounded path-reference occurrences per surface. Each
// surface uses the count variant matching what an apply would rewrite there:
// the JSON-escape variant on surfaces routed through the typed JSON helpers
// (history, sessions, config), the raw variant elsewhere. Transcripts and
// memory additionally count the absolute encoded storage-dir form, mirroring
// move's two-pass rewrite of those surfaces.
func countReferences(
	ctx context.Context,
	claudeHome *claude.Home,
	locations *claude.ProjectLocations,
) (map[string]int, error) {
	projectPath := locations.ProjectPath
	encodedDir := locations.ProjectDir
	counts := make(map[string]int)

	historyCount, err := countHistoryReferences(ctx, claudeHome, projectPath)
	if err != nil {
		return nil, err
	}
	counts["history"] = historyCount

	sessionsCount, err := countAcrossFiles(ctx, locations.SessionFiles, projectPath, rewrite.CountPathInBytesWithJSONEscape)
	if err != nil {
		return nil, err
	}
	counts["sessions"] = sessionsCount

	transcriptFiles, err := claude.TranscriptFiles(ctx, encodedDir)
	if err != nil {
		return nil, err
	}
	transcriptsCount, err := countWithEncodedDir(ctx, transcriptFiles, projectPath, encodedDir)
	if err != nil {
		return nil, err
	}
	counts["transcripts"] = transcriptsCount

	memoryCount, err := countWithEncodedDir(ctx, locations.MemoryFiles, projectPath, encodedDir)
	if err != nil {
		return nil, err
	}
	counts["memory"] = memoryCount

	configCount, err := countConfigReferences(ctx, claudeHome, projectPath)
	if err != nil {
		return nil, err
	}
	counts["config"] = configCount

	for _, target := range claude.UserWideRewriteTargets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		targetCount, err := countOptionalFile(target.Path(claudeHome), projectPath, rewrite.CountPathInBytes)
		if err != nil {
			return nil, err
		}
		counts[target.Name] = targetCount
	}

	for group, path := range locations.AllFlatFiles() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path) //nolint:gosec // path from trusted ProjectLocations
		if err != nil {
			return nil, fmt.Errorf("read %s file %s: %w", group.Name, path, err)
		}
		counts[group.Name] += rewrite.CountPathInBytes(data, projectPath)
	}

	return counts, nil
}

// countVariant counts bounded occurrences of path in data; the raw and
// JSON-escape variants from package rewrite both satisfy it.
type countVariant func(data []byte, path string) int

// countAcrossFiles sums variant(data, path) over each file. The paths come from
// a ProjectLocations collector that already confirmed existence, so a read
// error here is unexpected and fails the command rather than degrading silently.
func countAcrossFiles(ctx context.Context, paths []string, path string, variant countVariant) (int, error) {
	total := 0
	for _, filePath := range paths {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		data, err := os.ReadFile(filePath) //nolint:gosec // path from trusted ProjectLocations
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", filePath, err)
		}
		total += variant(data, path)
	}
	return total, nil
}

// countWithEncodedDir sums, per file, the raw real-path count plus the raw
// encoded-storage-dir count. Apply rewrites both forms in transcripts and
// memory with the plain replacer, so the raw variant is used for each pass.
func countWithEncodedDir(ctx context.Context, paths []string, projectPath, encodedDir string) (int, error) {
	total := 0
	for _, filePath := range paths {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		data, err := os.ReadFile(filePath) //nolint:gosec // path from trusted ProjectLocations
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", filePath, err)
		}
		total += rewrite.CountPathInBytes(data, projectPath)
		total += rewrite.CountPathInBytes(data, encodedDir)
	}
	return total, nil
}

// countOptionalFile counts references in a user-wide file that may be absent;
// a missing file contributes zero, matching move's stat-gated rewrite loop.
func countOptionalFile(path, projectPath string, variant countVariant) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path from trusted Home derivation
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	return variant(data, projectPath), nil
}

// countHistoryReferences sums JSON-escape-aware occurrences across every
// well-formed history.jsonl line. Malformed lines are skipped — an apply
// preserves them verbatim, so they carry no rewritable references. The scan
// buffer is capped with claude.MaxHistoryLine, matching every other
// history.jsonl reader.
func countHistoryReferences(ctx context.Context, claudeHome *claude.Home, projectPath string) (int, error) {
	historyFile := claudeHome.HistoryFile()
	file, err := os.Open(historyFile) //nolint:gosec // path derived from trusted Home
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("open history.jsonl: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), claude.MaxHistoryLine)

	total := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var probe claude.HistoryEntry
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		total += rewrite.CountPathInBytesWithJSONEscape(line, projectPath)
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan history.jsonl: %w", err)
	}
	return total, nil
}

// countConfigReferences counts JSON-escape-aware occurrences across the whole
// ~/.claude.json — the occurrence-across-the-file lens, deliberately broader
// than what an apply rewrites. rewrite.UserConfig rewrites only occurrences
// inside the matched project block, so a path appearing in another project's
// mcpServers args/env or in a top-level field is counted here yet left untouched
// by a move.
func countConfigReferences(ctx context.Context, claudeHome *claude.Home, projectPath string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	data, err := os.ReadFile(claudeHome.ConfigFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read config file: %w", err)
	}
	return rewrite.CountPathInBytesWithJSONEscape(data, projectPath), nil
}
