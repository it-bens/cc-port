// Package rewrite provides substrate-generic path and file rewrite primitives.
package rewrite

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// EscapeSJSONKey escapes `\` and `.` so an arbitrary string can be used as a
// single key segment in an sjson path expression. Project paths contain `.`
// (e.g. "/Users/x/proj.v2"), which sjson would otherwise parse as nested keys.
//
// Exported because both the move rewrite path and the import config merge path
// build sjson expressions from user-supplied project paths; one source of truth
// avoids drift between the two call sites.
func EscapeSJSONKey(key string) string {
	key = strings.ReplaceAll(key, `\`, `\\`)
	key = strings.ReplaceAll(key, `.`, `\.`)
	return key
}

// isPathContinuationByte reports whether b can extend a path component.
// The `.` byte is excluded here: ReplacePathInBytes uses a two-byte lookahead
// so sentence-terminating dots (prose) can be distinguished from
// extension-separator dots (`.v2`, `.txt`).
func isPathContinuationByte(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z':
		return true
	case b >= 'a' && b <= 'z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '_' || b == '-':
		return true
	}
	return false
}

// isExtensionDotAt reports whether the '.' at data[dotIndex] introduces a
// filename extension rather than sentence-terminating punctuation.
// A run of consecutive dots is walked first, so `foo.` at EOF and `foo...`
// before whitespace are both prose. A dot is an extension separator only
// when the first non-dot byte is a path-component character (letter, digit,
// `_`, `-`). Whitespace, quotes, other punctuation, and EOF are prose.
func isExtensionDotAt(data []byte, dotIndex int) bool {
	cursor := dotIndex
	for cursor < len(data) && data[cursor] == '.' {
		cursor++
	}
	if cursor == len(data) {
		return false
	}
	return isPathContinuationByte(data[cursor])
}

// ReplacePathInBytes replaces occurrences of oldPath with newPath in data,
// but only when the match is bounded on the right by a non-path-continuation
// byte (or by the end of the buffer).
//
// This avoids the prefix-collision corruption that plain substring replacement
// causes: replacing "/a/myproject" inside "/a/myproject-extras" would otherwise
// produce "/a/renamed-extras", silently corrupting an unrelated project's data.
//
// It returns the resulting bytes and the number of replacements made.
func ReplacePathInBytes(data []byte, oldPath, newPath string) (rewritten []byte, count int) {
	if oldPath == "" || len(data) == 0 {
		return append([]byte(nil), data...), 0
	}

	oldBytes := []byte(oldPath)
	newBytes := []byte(newPath)

	var result bytes.Buffer
	result.Grow(len(data))

	cursor := 0
	for cursor <= len(data)-len(oldBytes) {
		if !bytes.Equal(data[cursor:cursor+len(oldBytes)], oldBytes) {
			result.WriteByte(data[cursor])
			cursor++
			continue
		}

		// Boundary check: the byte AFTER the match must not extend the path
		// component. A '.' is handled specially — it only blocks the match
		// when it introduces a real extension (e.g. ".v2", ".txt"); a dot
		// followed by whitespace, punctuation, or EOF is prose (end of
		// sentence) and must not suppress the rewrite.
		nextIndex := cursor + len(oldBytes)
		if nextIndex < len(data) {
			nextByte := data[nextIndex]
			if nextByte == '.' {
				if isExtensionDotAt(data, nextIndex) {
					result.WriteByte(data[cursor])
					cursor++
					continue
				}
			} else if isPathContinuationByte(nextByte) {
				result.WriteByte(data[cursor])
				cursor++
				continue
			}
		}

		result.Write(newBytes)
		cursor = nextIndex
		count++
	}
	if cursor < len(data) {
		result.Write(data[cursor:])
	}

	return result.Bytes(), count
}

// TOMLPathRewrite rewrites bounded path references in raw TOML while preserving
// the original formatting and comments. It rejects paths that TOML basic-string
// keys would require escaping and verifies that only projects subkeys changed.
func TOMLPathRewrite(data []byte, oldPath, newPath string) (rewritten []byte, count int, err error) {
	if strings.ContainsAny(oldPath, `"\\`) || strings.ContainsAny(newPath, `"\\`) {
		return data, 0, fmt.Errorf("TOML path rewrite refuses paths containing a quote or backslash")
	}

	inputPaths, err := tomlKeyPaths(data)
	if err != nil {
		return data, 0, fmt.Errorf("validate TOML path rewrite input: %w", err)
	}

	rewritten, count = ReplacePathInBytes(data, oldPath, newPath)
	outputPaths, err := tomlKeyPaths(rewritten)
	if err != nil {
		return data, 0, fmt.Errorf("validate TOML path rewrite output: %w", err)
	}

	if !equalKeyPathMultisets(expectedTOMLKeyPaths(inputPaths, oldPath, newPath), outputPaths) {
		return data, 0, fmt.Errorf("validate TOML path rewrite: key paths changed outside projects")
	}
	return rewritten, count, nil
}

func tomlKeyPaths(data []byte) (map[string]int, error) {
	var document map[string]any
	if err := toml.Unmarshal(data, &document); err != nil {
		return nil, err
	}

	paths := make(map[string]int)
	collectTOMLKeyPaths(document, nil, paths)
	return paths, nil
}

func collectTOMLKeyPaths(value any, prefix []string, paths map[string]int) {
	document, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key, child := range document {
		path := append(append([]string(nil), prefix...), key)
		paths[encodeTOMLKeyPath(path)]++
		collectTOMLKeyPaths(child, path, paths)
	}
}

func expectedTOMLKeyPaths(paths map[string]int, oldPath, newPath string) map[string]int {
	expected := make(map[string]int, len(paths))
	for encoded, count := range paths {
		path := decodeTOMLKeyPath(encoded)
		if len(path) > 1 && path[0] == "projects" {
			rewritten, replacements := ReplacePathInBytes([]byte(path[1]), oldPath, newPath)
			if replacements > 0 {
				path[1] = string(rewritten)
			}
		}
		expected[encodeTOMLKeyPath(path)] += count
	}
	return expected
}

func encodeTOMLKeyPath(path []string) string {
	var encoded strings.Builder
	for _, segment := range path {
		encoded.WriteString(strconv.Itoa(len(segment)))
		encoded.WriteByte(':')
		encoded.WriteString(segment)
	}
	return encoded.String()
}

func decodeTOMLKeyPath(encoded string) []string {
	var path []string
	for encoded != "" {
		separator := strings.IndexByte(encoded, ':')
		length, err := strconv.Atoi(encoded[:separator])
		if err != nil || length < 0 || separator+1+length > len(encoded) {
			panic("invalid TOML key path encoding")
		}
		start := separator + 1
		path = append(path, encoded[start:start+length])
		encoded = encoded[start+length:]
	}
	return path
}

func equalKeyPathMultisets(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for path, leftCount := range left {
		if right[path] != leftCount {
			return false
		}
	}
	return true
}

// ReplacePathInBytesWithJSONEscape runs the boundary-aware rewriter twice:
// once against the raw path, once against the JSON-escaped form where every
// "/" becomes "\/". The boundary check applies to each pass independently.
// Output is byte-identical to ReplacePathInBytes when data contains no
// JSON-escaped forward slashes.
func ReplacePathInBytesWithJSONEscape(data []byte, oldPath, newPath string) (rewritten []byte, count int) {
	first, count1 := ReplacePathInBytes(data, oldPath, newPath)
	if !bytes.Contains(first, []byte(`\/`)) {
		return first, count1
	}
	escapedOld := strings.ReplaceAll(oldPath, "/", `\/`)
	escapedNew := strings.ReplaceAll(newPath, "/", `\/`)
	second, count2 := ReplacePathInBytes(first, escapedOld, escapedNew)
	return second, count1 + count2
}

// ContainsBoundedPath reports whether data contains at least one occurrence
// of path bounded on the right by a non-path-continuation byte — the same
// boundary rule ReplacePathInBytes uses when deciding whether a match is a
// real, independent path reference rather than a prefix of some unrelated
// path (e.g. "/a/myproject" inside "/a/myproject-extras").
//
// Exported because both the export extractor (filtering history lines that
// belong to a project) and the move rewriter (rewriting the same lines)
// need to agree on what counts as a bounded reference. Keeping a single
// source of truth in this package avoids drift between the two call sites.
func ContainsBoundedPath(data []byte, path string) bool {
	_, count := ReplacePathInBytes(data, path, path)
	return count > 0
}

// CountPathInBytes returns how many times path occurs in data as a bounded
// reference, using the same right-boundary rule as ReplacePathInBytes. It scans
// without materializing a rewritten copy, so stats can count occurrences across
// many files without the replacer's per-file buffer.
func CountPathInBytes(data []byte, path string) int {
	return countBoundedPath(data, path)
}

// CountPathInBytesWithJSONEscape counts bounded occurrences of path in both its
// raw form and its JSON-escaped form (each "/" written as "\/"), mirroring
// ReplacePathInBytesWithJSONEscape's two-pass coverage. It is the counting lens
// for surfaces an apply rewrites through the typed JSON helpers, whose emitters
// can escape forward slashes.
func CountPathInBytesWithJSONEscape(data []byte, path string) int {
	count := countBoundedPath(data, path)
	if !bytes.Contains(data, []byte(`\/`)) {
		return count
	}
	escaped := strings.ReplaceAll(path, "/", `\/`)
	return count + countBoundedPath(data, escaped)
}

// countBoundedPath walks data and counts matches of path bounded on the right
// by a non-path-continuation byte. The cursor advances exactly as
// ReplacePathInBytes's does (one byte past a rejected match, past the whole
// match on a hit) so the two stay in lock-step; the shared boundary predicates
// keep the rule itself single-sourced.
func countBoundedPath(data []byte, path string) int {
	if path == "" || len(data) == 0 {
		return 0
	}

	pathBytes := []byte(path)
	count := 0
	cursor := 0
	for cursor <= len(data)-len(pathBytes) {
		if !bytes.Equal(data[cursor:cursor+len(pathBytes)], pathBytes) {
			cursor++
			continue
		}

		nextIndex := cursor + len(pathBytes)
		if nextIndex < len(data) {
			nextByte := data[nextIndex]
			if nextByte == '.' {
				if isExtensionDotAt(data, nextIndex) {
					cursor++
					continue
				}
			} else if isPathContinuationByte(nextByte) {
				cursor++
				continue
			}
		}

		count++
		cursor = nextIndex
	}
	return count
}

// renameEntry captures one pending rename operation handled by
// SafeRenamePromoter. `temp` is the already-staged path that Promote will
// move into `final`. If `final` already exists at promote time, its bytes
// are backed up so Rollback can restore them; otherwise `existed` stays
// false and Rollback removes the promoted file.
type renameEntry struct {
	temp, final string
	// promoted is set to true once Promote has moved temp → final.
	promoted bool
	// existed captures whether `final` had content before promotion.
	existed bool
	// backupBytes are the pre-promote contents of `final`, saved if it
	// existed at promote time.
	backupBytes []byte
	// backupMode is the pre-promote mode of `final`.
	backupMode os.FileMode
	// isDir is true when the entry represents a directory rename rather
	// than a file rename. Directory backups are stored as temp paths on
	// disk (see backupDir) because in-memory buffering is unbounded.
	isDir bool
	// backupDir is the path where the replaced directory (if any) was
	// relocated before the promote. On rollback, it is renamed back into
	// place. Empty when `final` did not previously exist or when the
	// entry is a file.
	backupDir string
}

// SafeRenamePromoter stages a sequence of rename operations and applies them
// atomically from the caller's perspective. Each entry is a
// (temp, final) pair; Stage records the intent, Promote renames each temp
// onto its final in registration order (saving any displaced content as a
// backup), and Rollback walks the promoted entries in reverse order,
// restoring backups and removing any content that did not previously exist.
//
// The promoter is designed for the import stage-and-swap: every destination
// touched by a successful import is visible all-or-nothing, and any failure
// mid-sequence leaves the filesystem in its pre-import state except in
// catastrophic multi-fault cases.
//
// Rename atomicity is an `os.Rename` property and only holds when temp and
// final share a filesystem. Callers are responsible for choosing temp paths
// adjacent to their destinations; SafeRenamePromoter does not copy across
// filesystems.
type SafeRenamePromoter struct {
	entries []*renameEntry
	// renameFunc is the hook tests use to inject promote-time failures. In
	// production it is nil and os.Rename is called directly.
	renameFunc func(oldpath, newpath string) error
}

// NewSafeRenamePromoter returns a promoter ready to accept Stage calls.
func NewSafeRenamePromoter() *SafeRenamePromoter {
	return &SafeRenamePromoter{}
}

// SetRenameFunc overrides the underlying rename implementation. Package
// tests use this to inject EXDEV-like failures; production callers leave
// it at the zero value so os.Rename is used directly.
func (p *SafeRenamePromoter) SetRenameFunc(fn func(oldpath, newpath string) error) {
	p.renameFunc = fn
}

// StageFile records an intent to rename a staged file at temp onto final at
// Promote time. If final already exists, its bytes are read and kept as a
// backup for Rollback.
func (p *SafeRenamePromoter) StageFile(temp, final string) {
	p.entries = append(p.entries, &renameEntry{temp: temp, final: final})
}

// StageDir records an intent to rename a staged directory at temp onto
// final at Promote time. If final already exists at promote time, it is
// relocated to a sibling backup path so Rollback can restore it.
func (p *SafeRenamePromoter) StageDir(temp, final string) {
	p.entries = append(p.entries, &renameEntry{temp: temp, final: final, isDir: true})
}

// Promote applies each staged rename in order. On the first failure, it
// calls Rollback and returns the promote error; callers should not invoke
// Rollback themselves in that case.
func (p *SafeRenamePromoter) Promote() error {
	for _, entry := range p.entries {
		if err := p.promoteEntry(entry); err != nil {
			p.Rollback()
			return fmt.Errorf("promote %s: %w", entry.final, err)
		}
	}
	return nil
}

// promoteEntry moves a single staged temp onto its final, snapshotting any
// displaced content for rollback first.
func (p *SafeRenamePromoter) promoteEntry(entry *renameEntry) error {
	info, err := os.Stat(entry.final)
	switch {
	case err == nil:
		entry.existed = true
		entry.backupMode = info.Mode()
		if entry.isDir {
			backupDir := entry.final + ".cc-port-rollback"
			if renameErr := p.doRename(entry.final, backupDir); renameErr != nil {
				return fmt.Errorf("stash existing directory: %w", renameErr)
			}
			entry.backupDir = backupDir
		} else {
			data, readErr := os.ReadFile(entry.final)
			if readErr != nil {
				return fmt.Errorf("read existing file for backup: %w", readErr)
			}
			entry.backupBytes = data
		}
	case errors.Is(err, fs.ErrNotExist):
		entry.existed = false
	default:
		return fmt.Errorf("stat final: %w", err)
	}

	if err := p.doRename(entry.temp, entry.final); err != nil {
		// Best-effort restore of any backup already relocated for this entry.
		if entry.isDir && entry.backupDir != "" {
			_ = p.doRename(entry.backupDir, entry.final)
		}
		return err
	}
	entry.promoted = true
	return nil
}

func (p *SafeRenamePromoter) doRename(oldpath, newpath string) error {
	if p.renameFunc != nil {
		return p.renameFunc(oldpath, newpath)
	}
	return os.Rename(oldpath, newpath)
}

// Rollback walks promoted entries in reverse order and restores the
// pre-promote state on disk: backups are renamed or rewritten into their
// finals, and content that did not previously exist is removed. Best
// effort: errors from a single entry's restore are swallowed and the
// remaining entries are still attempted.
func (p *SafeRenamePromoter) Rollback() {
	for index := len(p.entries) - 1; index >= 0; index-- {
		entry := p.entries[index]
		if !entry.promoted {
			// Not yet promoted; best-effort clean up the temp if it still
			// exists. Ignore errors — the import is already failing.
			if entry.isDir {
				_ = os.RemoveAll(entry.temp)
			} else {
				_ = os.Remove(entry.temp)
			}
			continue
		}
		p.rollbackEntry(entry)
	}
}

func (p *SafeRenamePromoter) rollbackEntry(entry *renameEntry) {
	if !entry.existed {
		if entry.isDir {
			_ = os.RemoveAll(entry.final)
		} else {
			_ = os.Remove(entry.final)
		}
		return
	}

	if entry.isDir {
		_ = os.RemoveAll(entry.final)
		_ = p.doRename(entry.backupDir, entry.final)
		return
	}

	_ = SafeWriteFile(entry.final, entry.backupBytes, entry.backupMode)
}

// SafeWriteFile writes data to a temporary file in the same directory as path,
// then renames it to path. This provides an atomic write on most file systems.
// The temporary file is removed on error.
func SafeWriteFile(path string, data []byte, permissions os.FileMode) error {
	directory := filepath.Dir(path)

	temporaryFile, err := os.CreateTemp(directory, ".tmp-")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporaryFile.Name()

	_, writeErr := temporaryFile.Write(data)
	closeErr := temporaryFile.Close()

	if writeErr != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("write temporary file: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close temporary file: %w", closeErr)
	}

	if err := os.Chmod(temporaryPath, permissions); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("set permissions on temporary file: %w", err)
	}

	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("rename temporary file to destination: %w", err)
	}

	return nil
}
