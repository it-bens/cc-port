package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	ProjectPath          string
	ProjectDir           string
	SessionTranscripts   []string
	SessionSubdirs       []string
	MemoryFiles          []string
	FileHistoryDirs      []string
	SessionFiles         []string
	HistoryEntryCount    int
	HasConfigBlock       bool
	TodoFiles            []string
	UsageDataSessionMeta []string
	UsageDataFacets      []string
	PluginsDataFiles     []string
	TaskFiles            []string
}

// LocateProject enumerates all data locations for the given project path under
// the provided Home. It returns an error if the project directory does
// not exist. Optional resources (memory files, history entries, etc.) are
// collected with zero values when absent.
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

	if err := collectTodos(locations, claudeHome, sessionUUIDs); err != nil {
		return nil, err
	}

	if err := collectUsageData(locations, claudeHome, sessionUUIDs); err != nil {
		return nil, err
	}

	if err := collectPluginsData(locations, claudeHome, sessionUUIDs); err != nil {
		return nil, err
	}

	if err := collectTaskFiles(locations, claudeHome, sessionUUIDs); err != nil {
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

// collectTodos enumerates ~/.claude/todos/<sid1>-agent-<sid2>.json files,
// including any file where either UUID is in sessionUUIDs.
//
// The two-UUID filename pattern admits sub-agent spawns: the parent agent
// embeds its own session UUID and its child's session UUID in the filename.
// Including a file when either UUID matches catches todo state for sub-agents
// whose parent session belongs to the project; in practice the two UUIDs are
// equal in every existing file, but the format admits the divergent case.
func collectTodos(locations *ProjectLocations, claudeHome *Home, sessionUUIDs []string) error {
	todosDir := claudeHome.TodosDir()
	entries, err := os.ReadDir(todosDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read todos directory: %w", err)
	}

	uuidSet := make(map[string]struct{}, len(sessionUUIDs))
	for _, uuid := range sessionUUIDs {
		uuidSet[uuid] = struct{}{}
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		base := strings.TrimSuffix(name, ".json")
		parts := strings.SplitN(base, "-agent-", 2)
		if len(parts) != 2 {
			continue
		}
		if !uuidPattern.MatchString(parts[0]) || !uuidPattern.MatchString(parts[1]) {
			continue
		}
		if _, ok := uuidSet[parts[0]]; ok {
			locations.TodoFiles = append(locations.TodoFiles, filepath.Join(todosDir, name))
			continue
		}
		if _, ok := uuidSet[parts[1]]; ok {
			locations.TodoFiles = append(locations.TodoFiles, filepath.Join(todosDir, name))
		}
	}
	return nil
}

// collectUsageData enumerates ~/.claude/usage-data/{session-meta,facets}/<sid>.json
// for each session UUID in the project's set. Both subdirectories are checked
// independently — either may exist without the other on older Claude Code
// installs.
func collectUsageData(locations *ProjectLocations, claudeHome *Home, sessionUUIDs []string) error {
	base := claudeHome.UsageDataDir()
	for _, subdir := range []struct {
		name string
		dest *[]string
	}{
		{"session-meta", &locations.UsageDataSessionMeta},
		{"facets", &locations.UsageDataFacets},
	} {
		dir := filepath.Join(base, subdir.name)
		if _, err := os.Stat(dir); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat usage-data/%s: %w", subdir.name, err)
		}
		for _, uuid := range sessionUUIDs {
			candidate := filepath.Join(dir, uuid+".json")
			if _, err := os.Stat(candidate); err == nil {
				*subdir.dest = append(*subdir.dest, candidate)
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("stat usage-data/%s/%s: %w", subdir.name, uuid, err)
			}
		}
	}
	return nil
}

// collectPluginsData enumerates every file under ~/.claude/plugins/data/<ns>/<sid>/
// where <sid> is in the project's session set. Plugin namespace <ns> is opaque
// — the walk visits every namespace and treats them identically. The subtree
// is flattened to a list of absolute file paths so downstream consumers see a
// uniform shape across session-keyed groups.
func collectPluginsData(locations *ProjectLocations, claudeHome *Home, sessionUUIDs []string) error {
	base := claudeHome.PluginsDataDir()
	namespaces, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read plugins/data directory: %w", err)
	}

	uuidSet := make(map[string]struct{}, len(sessionUUIDs))
	for _, uuid := range sessionUUIDs {
		uuidSet[uuid] = struct{}{}
	}

	for _, namespace := range namespaces {
		if !namespace.IsDir() {
			continue
		}
		namespaceDir := filepath.Join(base, namespace.Name())
		sessionEntries, err := os.ReadDir(namespaceDir)
		if err != nil {
			return fmt.Errorf("read plugins/data/%s: %w", namespace.Name(), err)
		}
		for _, sessionEntry := range sessionEntries {
			if !sessionEntry.IsDir() {
				continue
			}
			if _, ok := uuidSet[sessionEntry.Name()]; !ok {
				continue
			}
			sessionDir := filepath.Join(namespaceDir, sessionEntry.Name())
			if err := appendFilesRecursive(&locations.PluginsDataFiles, sessionDir); err != nil {
				return fmt.Errorf("walk plugins/data/%s/%s: %w",
					namespace.Name(), sessionEntry.Name(), err)
			}
		}
	}
	return nil
}

// collectTaskFiles enumerates every non-directory entry under ~/.claude/tasks/<sid>/
// where <sid> is in the project's session set, including `.lock` and
// `.highwatermark` sidecars. This collector deliberately reports every file;
// consumers iterating via the registry apply the sidecar filter so the policy
// decision lives in one place.
func collectTaskFiles(locations *ProjectLocations, claudeHome *Home, sessionUUIDs []string) error {
	base := claudeHome.TasksDir()
	for _, uuid := range sessionUUIDs {
		candidate := filepath.Join(base, uuid)
		info, err := os.Stat(candidate)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat tasks/%s: %w", uuid, err)
		}
		if !info.IsDir() {
			continue
		}
		if err := appendFilesRecursive(&locations.TaskFiles, candidate); err != nil {
			return fmt.Errorf("walk tasks/%s: %w", uuid, err)
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
	scanner.Buffer(make([]byte, 64<<10), MaxHistoryLine)
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

// appendFilesRecursive appends every non-directory entry under dir to *dst.
// Filtering (e.g. sidecars) is the caller's responsibility.
func appendFilesRecursive(dst *[]string, dir string) error {
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		*dst = append(*dst, path)
		return nil
	})
}
