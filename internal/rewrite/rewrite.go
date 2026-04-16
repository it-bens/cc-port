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

	"github.com/it-bens/cc-port/internal/claude"
)

// ReplaceInBytes replaces all occurrences of oldString with newString in data.
// It returns the resulting bytes and the number of replacements made.
func ReplaceInBytes(data []byte, oldString, newString string) ([]byte, int) {
	count := strings.Count(string(data), oldString)
	result := bytes.ReplaceAll(data, []byte(oldString), []byte(newString))
	return result, count
}

// isPathContinuationByte reports whether b can extend a path component name —
// i.e. whether seeing b immediately after a candidate match means the match is
// actually a longer, different path (e.g. "myproject" vs "myproject-extras").
//
// Path component characters in practice: letters, digits, '_', '.', '-'.
func isPathContinuationByte(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z':
		return true
	case b >= 'a' && b <= 'z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '_' || b == '.' || b == '-':
		return true
	}
	return false
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

		// Boundary check: the byte AFTER the match must not be a path-continuation byte.
		nextIndex := cursor + len(oldBytes)
		if nextIndex < len(data) && isPathContinuationByte(data[nextIndex]) {
			result.WriteByte(data[cursor])
			cursor++
			continue
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

// HistoryJSONL processes a JSONL file line by line. For each well-formed line,
// it rewrites occurrences of oldProject to newProject — both the structured
// `project` field AND any free-text reference (e.g. inside `display`, inside
// `pastedContents`) — using path-boundary-aware substring replacement so that
// unrelated paths sharing a prefix (e.g. "myproject-extras") are not corrupted.
//
// The int return is the count of lines whose contents changed. Malformed lines
// are preserved verbatim (export tolerates them too — see export.extractProjectHistory).
// Empty lines and the trailing newline are preserved.
func HistoryJSONL(data []byte, oldProject, newProject string) ([]byte, int, error) {
	lines := bytes.Split(data, []byte("\n"))

	var outputLines [][]byte
	count := 0

	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			outputLines = append(outputLines, line)
			continue
		}

		var probe claude.HistoryEntry
		if err := json.Unmarshal(line, &probe); err != nil {
			// Malformed line — preserve verbatim, do not abort the whole file.
			outputLines = append(outputLines, append([]byte(nil), line...))
			continue
		}

		rewritten, replaced := ReplacePathInBytes(line, oldProject, newProject)
		if replaced > 0 {
			count++
		}
		outputLines = append(outputLines, rewritten)
	}

	return bytes.Join(outputLines, []byte("\n")), count, nil
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

// UserConfig parses ~/.claude.json and re-keys the project entry from
// oldProject to newProject in the projects map. Path references embedded in
// the block's contents (e.g. mcpServers.*.args, mcpServers.*.env.*,
// mcpContextUris, exampleFiles) are rewritten with path-boundary-aware
// substitution so values that hard-coded the old project path follow the
// rename.
//
// The bool return indicates whether the old key was found and moved. Other
// project keys and all Extra fields are preserved unchanged.
func UserConfig(data []byte, oldProject, newProject string) ([]byte, bool, error) {
	var userConfig claude.UserConfig
	if err := json.Unmarshal(data, &userConfig); err != nil {
		return nil, false, fmt.Errorf("unmarshal user config: %w", err)
	}

	projectData, found := userConfig.Projects[oldProject]
	if !found {
		result, err := json.Marshal(userConfig)
		if err != nil {
			return nil, false, fmt.Errorf("marshal user config: %w", err)
		}
		return result, false, nil
	}

	delete(userConfig.Projects, oldProject)
	if userConfig.Projects == nil {
		userConfig.Projects = make(map[string]json.RawMessage)
	}
	rewrittenProjectData, _ := ReplacePathInBytes(projectData, oldProject, newProject)
	userConfig.Projects[newProject] = rewrittenProjectData

	result, err := json.Marshal(userConfig)
	if err != nil {
		return nil, false, fmt.Errorf("marshal user config: %w", err)
	}
	return result, true, nil
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
