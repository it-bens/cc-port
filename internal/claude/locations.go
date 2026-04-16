package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// uuidPattern matches the canonical 8-4-4-4-12 UUID string format,
// case-insensitive. Version and variant nibbles are not enforced — Claude Code
// has used different UUID variants for session IDs across versions, so we
// accept any.
var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ProjectLocations holds all data locations for a specific project.
type ProjectLocations struct {
	ProjectPath        string
	ProjectDir         string
	SessionsIndex      string
	SessionTranscripts []string
	SessionSubdirs     []string
	MemoryFiles        []string
	FileHistoryDirs    []string
	SessionFiles       []string
	HistoryEntryCount  int
	HasConfigBlock     bool
}

// LocateProject enumerates all data locations for the given project path under
// the provided Home. It returns an error if the project directory does
// not exist. Optional resources (sessions-index.json, memory files, history
// entries, etc.) are collected with zero values when absent.
func LocateProject(claudeHome *Home, projectPath string) (*ProjectLocations, error) {
	projectDir := claudeHome.ProjectDir(projectPath)

	if _, err := os.Stat(projectDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("project directory not found: %s", projectDir)
		}
		return nil, fmt.Errorf("stat project directory: %w", err)
	}

	locations := &ProjectLocations{
		ProjectPath: projectPath,
		ProjectDir:  projectDir,
	}

	if err := collectSessionsIndex(locations, projectDir); err != nil {
		return nil, err
	}

	sessionUUIDs, err := collectProjectDirEntries(locations, projectDir)
	if err != nil {
		return nil, err
	}

	if err := collectMemoryFiles(locations, projectDir); err != nil {
		return nil, err
	}

	if err := collectFileHistoryDirs(locations, claudeHome, sessionUUIDs); err != nil {
		return nil, err
	}

	if err := collectSessionFiles(locations, claudeHome, projectPath); err != nil {
		return nil, err
	}

	if err := countHistoryEntries(locations, claudeHome, projectPath); err != nil {
		return nil, err
	}

	if err := checkConfigBlock(locations, claudeHome, projectPath); err != nil {
		return nil, err
	}

	return locations, nil
}

func collectSessionsIndex(locations *ProjectLocations, projectDir string) error {
	sessionsIndexPath := filepath.Join(projectDir, "sessions-index.json")
	if _, err := os.Stat(sessionsIndexPath); err == nil {
		locations.SessionsIndex = sessionsIndexPath
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat sessions-index.json: %w", err)
	}
	return nil
}

// collectProjectDirEntries reads the project directory, populating
// SessionTranscripts and SessionSubdirs. It returns the set of session UUIDs
// discovered from transcript filenames and subdirectory names.
func collectProjectDirEntries(locations *ProjectLocations, projectDir string) ([]string, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("read project directory: %w", err)
	}

	uuidSet := make(map[string]struct{})

	for _, entry := range entries {
		name := entry.Name()

		if entry.IsDir() {
			if name == "memory" || name == "sessions" {
				continue
			}
			if uuidPattern.MatchString(name) {
				locations.SessionSubdirs = append(locations.SessionSubdirs, filepath.Join(projectDir, name))
				uuidSet[name] = struct{}{}
			}
			continue
		}

		if strings.HasSuffix(name, ".jsonl") {
			locations.SessionTranscripts = append(locations.SessionTranscripts, filepath.Join(projectDir, name))
			uuid := strings.TrimSuffix(name, ".jsonl")
			if uuidPattern.MatchString(uuid) {
				uuidSet[uuid] = struct{}{}
			}
		}
	}

	sessionUUIDs := make([]string, 0, len(uuidSet))
	for uuid := range uuidSet {
		sessionUUIDs = append(sessionUUIDs, uuid)
	}
	return sessionUUIDs, nil
}

func collectMemoryFiles(locations *ProjectLocations, projectDir string) error {
	memoryDir := filepath.Join(projectDir, "memory")
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read memory directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			locations.MemoryFiles = append(locations.MemoryFiles, filepath.Join(memoryDir, entry.Name()))
		}
	}
	return nil
}

func collectFileHistoryDirs(locations *ProjectLocations, claudeHome *Home, sessionUUIDs []string) error {
	fileHistoryBase := claudeHome.FileHistoryDir()

	for _, uuid := range sessionUUIDs {
		candidate := filepath.Join(fileHistoryBase, uuid)
		if _, err := os.Stat(candidate); err == nil {
			locations.FileHistoryDirs = append(locations.FileHistoryDirs, candidate)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat file-history directory for session %s: %w", uuid, err)
		}
	}
	return nil
}

func collectSessionFiles(locations *ProjectLocations, claudeHome *Home, projectPath string) error {
	sessionsDir := claudeHome.SessionsDir()
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read sessions directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		sessionFilePath := filepath.Join(sessionsDir, entry.Name())
		data, err := os.ReadFile(sessionFilePath) //nolint:gosec // G304: path constructed from trusted sessions directory
		if err != nil {
			return fmt.Errorf("read session file %s: %w", entry.Name(), err)
		}

		var sessionFile SessionFile
		if err := json.Unmarshal(data, &sessionFile); err != nil {
			continue
		}

		if sessionFile.Cwd == projectPath {
			locations.SessionFiles = append(locations.SessionFiles, sessionFilePath)
		}
	}
	return nil
}

func countHistoryEntries(locations *ProjectLocations, claudeHome *Home, projectPath string) error {
	historyFilePath := claudeHome.HistoryFile()
	file, err := os.Open(historyFilePath) //nolint:gosec // G304: historyFilePath is derived from the trusted ClaudeHome
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open history file: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var historyEntry HistoryEntry
		if err := json.Unmarshal([]byte(line), &historyEntry); err != nil {
			continue
		}

		if historyEntry.Project == projectPath {
			locations.HistoryEntryCount++
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan history file: %w", err)
	}
	return nil
}

func checkConfigBlock(locations *ProjectLocations, claudeHome *Home, projectPath string) error {
	data, err := os.ReadFile(claudeHome.ConfigFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config file: %w", err)
	}

	var userConfig UserConfig
	if err := json.Unmarshal(data, &userConfig); err != nil {
		return nil
	}

	if _, exists := userConfig.Projects[projectPath]; exists {
		locations.HasConfigBlock = true
	}
	return nil
}
