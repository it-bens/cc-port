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

// SessionsIndex parses the sessions-index JSON, rewrites all entries where
// projectPath matches oldProject (setting projectPath to newProject and fullPath to
// newProjectDir), and re-serialises to JSON. The int return is the number of entries
// rewritten. Unknown fields are preserved via the Extra round-trip in SessionIndexEntry.
func SessionsIndex(data []byte, oldProject, newProject, oldProjectDir, newProjectDir string) ([]byte, int, error) {
	var sessionsIndex claude.SessionsIndex
	if err := json.Unmarshal(data, &sessionsIndex); err != nil {
		return nil, 0, fmt.Errorf("unmarshal sessions index: %w", err)
	}

	count := 0
	for index := range sessionsIndex.Entries {
		if sessionsIndex.Entries[index].ProjectPath == oldProject {
			sessionsIndex.Entries[index].ProjectPath = newProject
			sessionsIndex.Entries[index].FullPath = strings.Replace(
				sessionsIndex.Entries[index].FullPath,
				oldProjectDir,
				newProjectDir,
				1,
			)
			count++
		}
	}

	result, err := json.Marshal(sessionsIndex)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal sessions index: %w", err)
	}
	return result, count, nil
}

// HistoryJSONL processes a JSONL file line by line. For each line whose
// project field equals oldProject, the field is replaced with newProject.
// The int return is the count of lines rewritten. Empty lines are skipped but
// the trailing newline is preserved in the output.
func HistoryJSONL(data []byte, oldProject, newProject string) ([]byte, int, error) {
	lines := bytes.Split(data, []byte("\n"))

	var outputLines [][]byte
	count := 0

	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			outputLines = append(outputLines, line)
			continue
		}

		var historyEntry claude.HistoryEntry
		if err := json.Unmarshal(line, &historyEntry); err != nil {
			return nil, 0, fmt.Errorf("unmarshal history entry: %w", err)
		}

		if historyEntry.Project == oldProject {
			historyEntry.Project = newProject
			count++
		}

		rewritten, err := json.Marshal(historyEntry)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal history entry: %w", err)
		}
		outputLines = append(outputLines, rewritten)
	}

	return bytes.Join(outputLines, []byte("\n")), count, nil
}

// SessionFile parses a session JSON file and replaces the cwd field if it
// starts with oldProject. The bool return indicates whether cwd was changed.
// Unknown fields are preserved via the Extra round-trip in SessionFile.
func SessionFile(data []byte, oldProject, newProject string) ([]byte, bool, error) {
	var sessionFile claude.SessionFile
	if err := json.Unmarshal(data, &sessionFile); err != nil {
		return nil, false, fmt.Errorf("unmarshal session file: %w", err)
	}

	if !strings.HasPrefix(sessionFile.Cwd, oldProject) {
		result, err := json.Marshal(sessionFile)
		if err != nil {
			return nil, false, fmt.Errorf("marshal session file: %w", err)
		}
		return result, false, nil
	}

	sessionFile.Cwd = newProject + strings.TrimPrefix(sessionFile.Cwd, oldProject)
	result, err := json.Marshal(sessionFile)
	if err != nil {
		return nil, false, fmt.Errorf("marshal session file: %w", err)
	}
	return result, true, nil
}

// UserConfig parses ~/.claude.json and re-keys the project entry from
// oldProject to newProject in the projects map. The bool return indicates whether
// the old key was found and moved. Other project keys and all Extra fields are
// preserved unchanged.
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
	userConfig.Projects[newProject] = projectData

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
