package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/it-bens/cc-port/internal/rewrite"
)

// rolloutLineProbe reads just enough of a rollout JSONL line to classify
// it. Codex tags every RolloutItem line as {"type":…,"payload":…}
// (protocol/src/protocol.rs:3130-3145).
type rolloutLineProbe struct {
	Type string `json:"type"`
}

// rolloutTypeSessionMeta and rolloutTypeTurnContext are the two RolloutItem
// variants carrying the project's structured identity fields: session_meta
// (protocol/src/protocol.rs:3014-3062) and turn_context
// (protocol/src/protocol.rs:3208-3224).
const (
	rolloutTypeSessionMeta = "session_meta"
	rolloutTypeTurnContext = "turn_context"
)

// rolloutRoots lists the two physical roots a rollout can live under:
// sessions/YYYY/MM/DD/ and the flat archived_sessions/ (rollout/src/lib.rs:21-22).
// Archiving physically renames the file from one root to the other
// (thread-store/src/local/archive_thread.rs:41-53).
func rolloutRoots(home *Home) []string {
	return []string{
		filepath.Join(home.Dir, sessionsSubdir),
		filepath.Join(home.Dir, archivedSessionsSubdir),
	}
}

// discoverRolloutFiles walks both rollout roots and returns the LOGICAL
// rollout set, in sorted order: when both X.jsonl and its X.jsonl.zst
// sibling exist, only X.jsonl is kept. Codex's own compression worker can
// leave both on disk momentarily — it persists the compressed file before
// removing the plain one (rollout/src/compression.rs:632-651) — and never
// re-compresses once the plain file is gone, so a crash in that window
// strands the pair with no self-heal. Every data consumer (export, move,
// projectRollouts, knowsProject, stats) must see exactly one file per
// logical rollout or a duplicate archive entry corrupts the whole import;
// this mirrors Codex's own walker, which applies the identical suppression
// (rollout/src/compression.rs:141-163, should_skip_compressed_sibling at
// 941-943). The freshness witness needs every physical file's mtime, so it
// uses discoverRolloutFilesRaw instead. A missing root is not an error: a
// fresh Codex home may not have written it yet.
func discoverRolloutFiles(home *Home) ([]string, error) {
	files, err := discoverRolloutFilesRaw(home)
	if err != nil {
		return nil, err
	}
	return suppressCompressedSiblings(files), nil
}

// discoverRolloutFilesRaw returns every rollout .jsonl and .jsonl.zst file
// exactly as found on disk, in sorted order, including a .jsonl.zst
// crash-window sibling discoverRolloutFiles would suppress.
func discoverRolloutFilesRaw(home *Home) ([]string, error) {
	var files []string
	for _, root := range rolloutRoots(home) {
		found, err := listRolloutFiles(root)
		if err != nil {
			return nil, err
		}
		files = append(files, found...)
	}
	sort.Strings(files)
	return files, nil
}

// suppressCompressedSiblings drops every .jsonl.zst entry whose plain
// .jsonl counterpart is also present in files.
func suppressCompressedSiblings(files []string) []string {
	plain := make(map[string]bool, len(files))
	for _, path := range files {
		if !strings.HasSuffix(path, zstSuffix) {
			plain[path] = true
		}
	}
	var kept []string
	for _, path := range files {
		if strings.HasSuffix(path, zstSuffix) && plain[strings.TrimSuffix(path, zstSuffix)] {
			continue
		}
		kept = append(kept, path)
	}
	return kept
}

func listRolloutFiles(root string) ([]string, error) {
	var files []string
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl"+zstSuffix) {
			files = append(files, path)
		}
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("walk %s: %w", root, walkErr)
	}
	return files, nil
}

// hasStructuredCwd reports whether lines contains at least one
// session_meta or turn_context line. A rollout with neither predates
// structured cwd tracking (era-A): move must not touch it, even under
// --deep, since there is nothing to anchor a safe rewrite to.
func hasStructuredCwd(lines [][]byte) bool {
	for _, line := range lines {
		var probe rolloutLineProbe
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Type == rolloutTypeSessionMeta || probe.Type == rolloutTypeTurnContext {
			return true
		}
	}
	return false
}

// countRolloutLine returns how many bounded occurrences of oldPath a
// rewrite would touch in line. session_meta and turn_context lines are
// always counted; every other line (response items, world-state blobs,
// compacted summaries) is counted only under deep.
func countRolloutLine(line []byte, oldPath string, deep bool) int {
	var probe rolloutLineProbe
	if err := json.Unmarshal(line, &probe); err != nil {
		return 0
	}
	structured := probe.Type == rolloutTypeSessionMeta || probe.Type == rolloutTypeTurnContext
	if deep {
		return rewrite.CountPathInBytesWithJSONEscape(line, oldPath)
	}
	if !structured {
		return 0
	}
	return countStructuredRolloutFields(line, probe.Type, oldPath)
}

// rewriteRolloutLine rewrites one rollout JSONL line under the same
// always-structured / opt-in-prose rule as countRolloutLine, returning how
// many bounded occurrences it changed (not just whether the line changed,
// so the total agrees with countRolloutLine's occurrence count). A
// malformed or non-JSON line is left verbatim, matching the claude
// adapter's malformed-line-preserved-verbatim convention.
func rewriteRolloutLine(line []byte, oldPath, newPath string, deep bool) (rewritten []byte, count int) {
	var probe rolloutLineProbe
	if err := json.Unmarshal(line, &probe); err != nil {
		return line, 0
	}
	structured := probe.Type == rolloutTypeSessionMeta || probe.Type == rolloutTypeTurnContext
	if deep {
		return rewrite.ReplacePathInBytesWithJSONEscape(line, oldPath, newPath)
	}
	if !structured {
		return line, 0
	}
	return rewriteStructuredRolloutFields(line, probe.Type, oldPath, newPath)
}

func countStructuredRolloutFields(line []byte, rolloutType, oldPath string) int {
	total := 0
	for _, path := range structuredRolloutFieldPaths(line, rolloutType) {
		value := gjson.GetBytes(line, path)
		if value.Type == gjson.String {
			total += rewrite.CountPathInBytes([]byte(value.String()), oldPath)
		}
	}
	return total
}

//nolint:gocritic // Named results would be shadowed by the per-field rewrite values.
func rewriteStructuredRolloutFields(line []byte, rolloutType, oldPath, newPath string) ([]byte, int) {
	updated := line
	total := 0
	for _, path := range structuredRolloutFieldPaths(line, rolloutType) {
		value := gjson.GetBytes(updated, path)
		if value.Type != gjson.String {
			continue
		}
		rewritten, count := rewrite.ReplacePathInBytes([]byte(value.String()), oldPath, newPath)
		if count == 0 {
			continue
		}
		var err error
		updated, err = sjson.SetBytes(updated, path, string(rewritten))
		if err != nil {
			return line, 0
		}
		total += count
	}
	return updated, total
}

func structuredRolloutFieldPaths(line []byte, rolloutType string) []string {
	paths := []string{"payload.cwd"}
	if rolloutType != rolloutTypeTurnContext {
		return paths
	}
	roots := gjson.GetBytes(line, "payload.workspace_roots")
	if !roots.IsArray() {
		return paths
	}
	for index, root := range roots.Array() {
		if root.Type == gjson.String {
			paths = append(paths, "payload.workspace_roots."+strconv.Itoa(index))
		}
	}
	return paths
}

// planRolloutFile reports how many occurrences a move would rewrite in
// path, and whether path is era-A (no structured cwd, therefore skipped
// entirely regardless of deep).
func planRolloutFile(path, oldPath string, deep bool, caps TranscodeCaps) (count int, eraA bool, err error) {
	lines, _, err := readRolloutLines(path, caps)
	if err != nil {
		return 0, false, fmt.Errorf("read %s: %w", path, err)
	}
	if !hasStructuredCwd(lines) {
		return 0, true, nil
	}
	for _, line := range lines {
		count += countRolloutLine(line, oldPath, deep)
	}
	return count, false, nil
}

// applyRolloutFile rewrites path in place via TranscodeLines, unless path
// is era-A, in which case it is left untouched and eraA reports that.
func applyRolloutFile(ctx context.Context, path, oldPath, newPath string, deep bool, caps TranscodeCaps) (count int, eraA bool, err error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	lines, _, err := readRolloutLines(path, caps)
	if err != nil {
		return 0, false, fmt.Errorf("read %s: %w", path, err)
	}
	if !hasStructuredCwd(lines) {
		return 0, true, nil
	}
	changedCount, err := TranscodeLines(path, caps, func(line []byte) ([]byte, int) {
		return rewriteRolloutLine(line, oldPath, newPath, deep)
	})
	if err != nil {
		return 0, false, fmt.Errorf("transcode %s: %w", path, err)
	}
	return changedCount, false, nil
}
