// Package rewrite provides functions for rewriting Claude Code data files
// when a project is moved from one path to another.
package rewrite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/it-bens/cc-port/internal/claude"
)

// EscapeSJSONKey escapes the characters sjson treats as path meta-characters
// (`\` and `.`) so an arbitrary string can be used as a single key segment in
// an sjson path expression. Project paths routinely contain `.` (e.g.
// "/Users/x/proj.v2"), which would otherwise be parsed as nested keys.
//
// This is exported because both the move rewrite path and the import config
// merge path need to build sjson expressions from user-supplied project paths;
// keeping a single source of truth avoids drift between the two call sites.
func EscapeSJSONKey(key string) string {
	key = strings.ReplaceAll(key, `\`, `\\`)
	key = strings.ReplaceAll(key, `.`, `\.`)
	return key
}

// binaryMagicPrefixes is the shortlist of file-type signatures that IsLikelyText
// recognises as unambiguously binary. Anything starting with one of these byte
// sequences is classified as binary regardless of whether it happens to be
// null-free in the first few hundred bytes.
var binaryMagicPrefixes = [][]byte{
	{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, // PNG
	{0xff, 0xd8, 0xff},                            // JPEG
	{'%', 'P', 'D', 'F', '-'},                     // PDF
	{'P', 'K', 0x03, 0x04},                        // ZIP / JAR / etc.
	{0x1f, 0x8b},                                  // gzip
}

// windowLength is the size of each of the three windows IsLikelyText samples.
const windowLength = 512

// IsLikelyText is a binary-detection heuristic used to decide whether a file
// should be passed through path-substring rewriting. Substring replacement on
// binary payloads (file-history snapshots can be arbitrary bytes) would
// corrupt them. The heuristic has two stages:
//
//  1. Magic-byte fast path. If data starts with a prefix in binaryMagicPrefixes
//     (PNG, JPEG, PDF, ZIP, gzip), return false immediately. These formats
//     sometimes have long null-free headers that would otherwise fool stage 2.
//  2. Triple-window null-byte scan. Scan up to three 512-byte windows — at the
//     start, centred on the middle, and at the end of data. A null byte in any
//     window classifies the buffer as binary. Conventional UTF-8 text never
//     contains a null; three samples is a strong signal across files that have
//     a textual header but binary body (or the reverse).
//
// Residual false-positive risk: a text file whose middle or tail 512 bytes
// happen to contain a null byte (rare but possible) is classified as binary
// and therefore not rewritten. Residual false-negative risk: a binary format
// not in the magic shortlist (RAR, 7z, exotic containers) whose three windows
// all happen to be null-free is still treated as text. Both risks are
// documented in the known-limitations README.
func IsLikelyText(data []byte) bool {
	if len(data) == 0 {
		return true
	}

	for _, magic := range binaryMagicPrefixes {
		if bytes.HasPrefix(data, magic) {
			return false
		}
	}

	windows := sampleWindows(data, windowLength)
	for _, window := range windows {
		if bytes.ContainsRune(window, 0) {
			return false
		}
	}
	return true
}

// sampleWindows returns up to three byte slices of the requested length,
// sampled at the start, middle, and end of data. Windows may overlap when
// data is shorter than 3×length; that is fine — scanning overlapping bytes
// a second time is cheap and preserves the invariant that "null byte in any
// window means binary" works uniformly regardless of buffer length.
func sampleWindows(data []byte, length int) [][]byte {
	if len(data) <= length {
		return [][]byte{data}
	}

	head := data[:length]
	tail := data[len(data)-length:]

	middleStart := (len(data) - length) / 2
	middle := data[middleStart : middleStart+length]

	return [][]byte{head, middle, tail}
}

// isPathContinuationByte reports whether b can extend a path component name —
// i.e. whether seeing b immediately after a candidate match means the match is
// actually a longer, different path (e.g. "myproject" vs "myproject-extras").
//
// Path component characters in practice: letters, digits, '_', '-'. The `.`
// byte is deliberately excluded here: it is handled by a two-byte lookahead
// in ReplacePathInBytes so sentence-terminating dots (prose) can be
// distinguished from extension-separator dots (`.v2`, `.txt`).
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

// isExtensionDotAt reports whether the '.' byte at data[dotIndex] introduces
// a filename extension rather than sentence-terminating punctuation.
//
// A run of consecutive dots is walked past first, so `foo.` at EOF and
// `foo...` before whitespace are both classified as prose (no extension).
// A dot is an extension separator only when the first non-dot byte that
// follows is a filename character (letter, digit, '_', '-'). Anything else
// — whitespace, quotes, other punctuation, EOF — is prose.
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
// but only when the match is bounded on both sides by a non-path-continuation
// byte (or by the start/end of the buffer).
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

// HistoryJSONL processes a JSONL file line by line. For each well-formed line,
// it rewrites occurrences of oldProject to newProject — both the structured
// `project` field AND any free-text reference (e.g. inside `display`, inside
// `pastedContents`) — using path-boundary-aware substring replacement so that
// unrelated paths sharing a prefix (e.g. "myproject-extras") are not corrupted.
//
// Returns the rewritten bytes, the count of lines whose contents changed, and
// the 1-based line numbers of malformed (non-JSON) lines. Malformed lines are
// preserved verbatim — cc-port cannot reliably repair data that was already
// broken before the move. Callers should surface the returned line numbers to
// the user so the malformed entries can be inspected manually.
//
// Empty lines and the trailing newline are preserved.
func HistoryJSONL(data []byte, oldProject, newProject string) ([]byte, int, []int, error) {
	lines := bytes.Split(data, []byte("\n"))

	var outputLines [][]byte
	var malformedLineNumbers []int
	count := 0

	for lineIndex, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			outputLines = append(outputLines, line)
			continue
		}

		var probe claude.HistoryEntry
		if err := json.Unmarshal(line, &probe); err != nil {
			// Malformed line — preserve verbatim, do not abort the whole file.
			// Record the 1-based line number so callers can warn the user.
			malformedLineNumbers = append(malformedLineNumbers, lineIndex+1)
			outputLines = append(outputLines, append([]byte(nil), line...))
			continue
		}

		rewritten, replaced := ReplacePathInBytes(line, oldProject, newProject)
		if replaced > 0 {
			count++
		}
		outputLines = append(outputLines, rewritten)
	}

	return bytes.Join(outputLines, []byte("\n")), count, malformedLineNumbers, nil
}

// SessionFile rewrites every occurrence of oldProject to newProject inside
// the session JSON using path-boundary-aware substitution. The top-level
// `cwd` field is covered, as is any occurrence embedded elsewhere in the
// file — including nested values that JSON-decode into the preserved Extra
// map (free-form payload state that Claude Code sometimes writes alongside
// the core fields).
//
// The bool return indicates whether at least one occurrence was rewritten.
// Malformed JSON is rejected so callers can rely on the input being a
// well-formed session file before any bytes are returned.
func SessionFile(data []byte, oldProject, newProject string) ([]byte, bool, error) {
	var sessionFile claude.SessionFile
	if err := json.Unmarshal(data, &sessionFile); err != nil {
		return nil, false, fmt.Errorf("unmarshal session file: %w", err)
	}

	rewritten, count := ReplacePathInBytes(data, oldProject, newProject)
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

	rewrittenBlock, _ := ReplacePathInBytes([]byte(existing.Raw), oldProject, newProject)

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

// isPlaceholderKeyByte reports whether b is valid inside a placeholder key.
// Placeholder keys are `[A-Z0-9_]+` — the upper-snake shape every
// auto-detected and prompted key emitted by cc-port follows.
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
// catastrophic multi-fault cases (documented in the README).
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
	case os.IsNotExist(err):
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

// doRename invokes the promoter's rename hook (os.Rename by default).
func (p *SafeRenamePromoter) doRename(oldpath, newpath string) error {
	if p.renameFunc != nil {
		return p.renameFunc(oldpath, newpath)
	}
	return os.Rename(oldpath, newpath)
}

// Rollback walks promoted entries in reverse order and restores the
// pre-promote state on disk: backups are renamed or rewritten into their
// finals, and content that did not previously exist is removed. Best
// effort — a failure to restore one entry is logged by leaving the state
// as-is and continuing with the remaining entries.
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
