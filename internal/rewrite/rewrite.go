// Package rewrite provides functions for rewriting Claude Code data files
// when a project is moved from one path to another.
package rewrite

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/it-bens/cc-port/internal/claude"
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
func ReplacePathInBytes(data []byte, oldPath, newPath string) ([]byte, int) {
	if len(oldPath) == 0 || len(data) == 0 {
		return append([]byte(nil), data...), 0
	}

	oldBytes := []byte(oldPath)
	newBytes := []byte(newPath)

	var result bytes.Buffer
	result.Grow(len(data))

	count := 0
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

// ReplacePathInBytesWithJSONEscape runs the boundary-aware rewriter twice:
// once against the raw path, once against the JSON-escaped form where every
// "/" becomes "\/". The boundary check applies to each pass independently.
// Output is byte-identical to ReplacePathInBytes when data contains no
// JSON-escaped forward slashes.
func ReplacePathInBytesWithJSONEscape(data []byte, oldPath, newPath string) ([]byte, int) {
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

// StreamHistoryJSONL streams src line by line, rewriting every well-formed
// entry so oldProject becomes newProject (in the structured `project` field
// AND in any free-text reference such as `display` or `pastedContents`),
// using path-boundary-aware substring replacement so unrelated paths sharing
// a prefix (e.g. "myproject-extras") are not corrupted.
//
// Returns the count of lines whose contents changed and the 1-based line
// numbers of malformed (non-JSON) lines. Malformed lines are preserved
// verbatim — cc-port cannot reliably repair data that was already broken
// before the move. Callers should surface the line numbers so the malformed
// entries can be inspected manually.
//
// The output is byte-for-byte equivalent to the previous whole-file path:
// empty lines are preserved, and the trailing newline (or its absence) is
// mirrored from the input. Lines exceeding claude.MaxHistoryLine fail with
// bufio.ErrTooLong rather than being silently truncated.
//
// Cancellation: ctx is checked at each line boundary; a cancelled ctx
// short-circuits the stream and returns ctx.Err() after a best-effort flush.
func StreamHistoryJSONL(
	ctx context.Context,
	src io.Reader,
	dst io.Writer,
	oldProject, newProject string,
) (int, []int, error) {
	reader := bufio.NewReaderSize(src, 64<<10)
	writer := bufio.NewWriterSize(dst, 64<<10)

	count := 0
	var malformed []int
	lineNumber := 0
	for {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}

		line, readErr := reader.ReadBytes('\n')
		if len(line) > claude.MaxHistoryLine {
			return 0, nil, fmt.Errorf(
				"history.jsonl line %d exceeds %d bytes: %w",
				lineNumber+1, claude.MaxHistoryLine, bufio.ErrTooLong,
			)
		}

		if len(line) > 0 {
			lineNumber++
			body, terminator := splitLineTerminator(line)

			out, isMalformed := rewriteHistoryLine(body, oldProject, newProject, &count)
			if isMalformed {
				malformed = append(malformed, lineNumber)
			}

			if _, err := writer.Write(out); err != nil {
				return 0, nil, fmt.Errorf("write history line %d: %w", lineNumber, err)
			}
			if len(terminator) > 0 {
				if _, err := writer.Write(terminator); err != nil {
					return 0, nil, fmt.Errorf("write history line %d terminator: %w", lineNumber, err)
				}
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return 0, nil, fmt.Errorf("read history line %d: %w", lineNumber+1, readErr)
		}
	}

	if err := writer.Flush(); err != nil {
		return 0, nil, fmt.Errorf("flush history output: %w", err)
	}
	return count, malformed, nil
}

// splitLineTerminator separates a line read by bufio.Reader.ReadBytes('\n')
// into its body and the trailing '\n' terminator (empty when the last line
// has no terminator). Exposed separately so the rewrite loop never writes a
// terminator that was not present in the source.
func splitLineTerminator(line []byte) (body, terminator []byte) {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		return line[:len(line)-1], line[len(line)-1:]
	}
	return line, nil
}

// rewriteHistoryLine applies the per-line transform. Empty lines round-trip
// unchanged; malformed lines are preserved verbatim; well-formed lines go
// through ReplacePathInBytes and bump *count when at least one match lands.
func rewriteHistoryLine(body []byte, oldProject, newProject string, count *int) ([]byte, bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		return body, false
	}
	var probe claude.HistoryEntry
	if err := json.Unmarshal(body, &probe); err != nil {
		return body, true
	}
	rewritten, replaced := ReplacePathInBytesWithJSONEscape(body, oldProject, newProject)
	if replaced > 0 {
		*count++
	}
	return rewritten, false
}

// SessionFile rewrites every occurrence of oldProject to newProject inside
// the session JSON using path-boundary-aware substitution. The top-level
// `cwd` field is covered, as is any occurrence embedded elsewhere in the
// file, including nested values that JSON-decode into the preserved Extra map.
//
// Uses json.Unmarshal against the typed claude.SessionFile shape to validate
// the input: the typed validator rejects structurally invalid files before any
// bytes are rewritten. (UserConfig uses gjson.ValidBytes instead because sjson
// does the mutation and only byte-level JSON validity matters there.)
//
// The bool return indicates whether at least one occurrence was rewritten.
func SessionFile(data []byte, oldProject, newProject string) ([]byte, bool, error) {
	var sessionFile claude.SessionFile
	if err := json.Unmarshal(data, &sessionFile); err != nil {
		return nil, false, fmt.Errorf("unmarshal session file: %w", err)
	}

	rewritten, count := ReplacePathInBytesWithJSONEscape(data, oldProject, newProject)
	return rewritten, count > 0, nil
}

// UserConfig rewrites ~/.claude.json to re-key the project entry from
// oldProject to newProject. Path references embedded in the block's
// contents (e.g. mcpServers.*.args, mcpServers.*.env.*, mcpContextUris,
// exampleFiles) are rewritten with path-boundary-aware substitution so
// values that hard-coded the old project path follow the rename.
//
// The operation uses sjson to splice only the projects object, which
// preserves every byte outside the rekeyed entry — original key order,
// indent style, and trailing newlines all survive.
//
// The bool return indicates whether the old key was found and moved. Other
// project keys and top-level fields are left untouched.
func UserConfig(data []byte, oldProject, newProject string) ([]byte, bool, error) {
	if !gjson.ValidBytes(data) {
		return nil, false, fmt.Errorf("invalid user config JSON")
	}

	oldPath := "projects." + EscapeSJSONKey(oldProject)
	existing := gjson.GetBytes(data, oldPath)
	if !existing.Exists() {
		return data, false, nil
	}

	rewrittenBlock, _ := ReplacePathInBytesWithJSONEscape([]byte(existing.Raw), oldProject, newProject)

	updated, err := sjson.DeleteBytes(data, oldPath)
	if err != nil {
		return nil, false, fmt.Errorf("delete old project key: %w", err)
	}
	newPath := "projects." + EscapeSJSONKey(newProject)
	updated, err = sjson.SetRawBytes(updated, newPath, rewrittenBlock)
	if err != nil {
		return nil, false, fmt.Errorf("insert new project key: %w", err)
	}
	return updated, true, nil
}

// maxPlaceholderKeyLength bounds the number of bytes FindPlaceholderTokens
// looks ahead after `{{` for the matching `}}`. A real cc-port placeholder
// key is a short upper-snake identifier (`PROJECT_PATH`, `HOME`,
// `UNRESOLVED_1`); 64 is generous and prevents pathological scans on input
// that contains many `{{` sequences without a close.
const maxPlaceholderKeyLength = 64

// isPlaceholderKeyByte reports whether b is valid inside a placeholder key:
// `[A-Z0-9_]+`, the upper-snake shape every key emitted by cc-port follows.
func isPlaceholderKeyByte(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '_':
		return true
	}
	return false
}

// FindPlaceholderTokens returns every distinct placeholder token of the form
// `{{KEY}}` found in data, where KEY matches `[A-Z0-9_]+`. Tokens are
// returned with their surrounding braces included (e.g. `{{PROJECT_PATH}}`)
// in first-occurrence order.
//
// Exotic placeholder shapes — lowercase keys, punctuation, whitespace inside
// braces, nested braces, multi-line keys — are ignored by design. cc-port's
// export path only ever writes upper-snake keys, so matching anything wider
// would invite false positives on ordinary JSON or Markdown content that
// happens to contain `{{`.
func FindPlaceholderTokens(data []byte) []string {
	seen := make(map[string]struct{})
	var tokens []string

	for cursor := 0; cursor < len(data)-3; cursor++ {
		if data[cursor] != '{' || data[cursor+1] != '{' {
			continue
		}

		// Walk key bytes up to the length cap; bail on any non-key byte.
		keyEnd := cursor + 2
		for keyEnd < len(data) && keyEnd-cursor-2 < maxPlaceholderKeyLength {
			if !isPlaceholderKeyByte(data[keyEnd]) {
				break
			}
			keyEnd++
		}

		if keyEnd == cursor+2 {
			// No key bytes between braces — `{{}}` or `{{ `; skip.
			continue
		}
		if keyEnd+1 >= len(data) || data[keyEnd] != '}' || data[keyEnd+1] != '}' {
			// Not closed with `}}` immediately after the key.
			continue
		}

		token := string(data[cursor : keyEnd+2])
		if _, already := seen[token]; !already {
			seen[token] = struct{}{}
			tokens = append(tokens, token)
		}
		cursor = keyEnd + 1
	}
	return tokens
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
