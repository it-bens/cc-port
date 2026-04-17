package claude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EncodePath converts an absolute, symlink-resolved filesystem path into the
// directory-name form Claude Code uses under ~/.claude/projects/. The encoding
// is lossy (/, ., space all collapse to -); pass pre-resolved paths or the
// result will not match what Claude Code wrote.
func EncodePath(absPath string) string {
	replacer := strings.NewReplacer(
		"/", "-",
		".", "-",
		" ", "-",
	)
	return replacer.Replace(absPath)
}

// ResolveProjectPath normalises a user-supplied project path through symlinks
// so its encoded form matches what Claude Code wrote. Required for correctness
// on macOS and Linux, where /tmp is a symlink: without this step,
// cc-port move /tmp/foo would encode to -tmp-foo and miss the real directory.
func ResolveProjectPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("absolute path for %q: %w", path, err)
	}

	// Walk up from the full path to the longest prefix that exists on disk,
	// so nonexistent trailing components (e.g. a move destination that has
	// not been created yet) do not prevent symlink resolution of the parent.
	existingPrefix := absPath
	var missingSuffix string
	for {
		if _, err := os.Lstat(existingPrefix); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat %q: %w", existingPrefix, err)
		}
		if existingPrefix == "/" {
			break
		}
		parent, child := filepath.Split(existingPrefix)
		existingPrefix = strings.TrimSuffix(parent, "/")
		if existingPrefix == "" {
			existingPrefix = "/"
		}
		if missingSuffix == "" {
			missingSuffix = child
		} else {
			missingSuffix = filepath.Join(child, missingSuffix)
		}
	}

	resolvedPrefix, err := filepath.EvalSymlinks(existingPrefix)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks for %q: %w", existingPrefix, err)
	}

	if missingSuffix == "" {
		return resolvedPrefix, nil
	}
	return filepath.Join(resolvedPrefix, missingSuffix), nil
}

// Home represents the root of Claude Code's data storage.
type Home struct {
	Dir        string // Path to the ~/.claude directory.
	ConfigFile string // Path to the ~/.claude.json file.
}

// NewHome creates a Home. If override is empty, it uses the
// default locations derived from the user's home directory.
// If override is provided, it is used as the Dir and ConfigFile is derived
// as Dir + ".json".
func NewHome(override string) (*Home, error) {
	if override != "" {
		return &Home{
			Dir:        override,
			ConfigFile: override + ".json",
		}, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("determine home directory: %w", err)
	}

	return &Home{
		Dir:        filepath.Join(homeDir, ".claude"),
		ConfigFile: filepath.Join(homeDir, ".claude.json"),
	}, nil
}

// ProjectsDir returns the path to the ~/.claude/projects directory.
func (claudeHome *Home) ProjectsDir() string {
	return filepath.Join(claudeHome.Dir, "projects")
}

// ProjectDir returns the path to the project-specific directory under projects/.
func (claudeHome *Home) ProjectDir(projectPath string) string {
	return filepath.Join(claudeHome.ProjectsDir(), EncodePath(projectPath))
}

// HistoryFile returns the path to the history.jsonl file.
func (claudeHome *Home) HistoryFile() string {
	return filepath.Join(claudeHome.Dir, "history.jsonl")
}

// SessionsDir returns the path to the ~/.claude/sessions directory.
func (claudeHome *Home) SessionsDir() string {
	return filepath.Join(claudeHome.Dir, "sessions")
}

// SettingsFile returns the path to the ~/.claude/settings.json file.
func (claudeHome *Home) SettingsFile() string {
	return filepath.Join(claudeHome.Dir, "settings.json")
}

// RulesDir returns the path to the ~/.claude/rules directory.
func (claudeHome *Home) RulesDir() string {
	return filepath.Join(claudeHome.Dir, "rules")
}

// FileHistoryDir returns the path to the ~/.claude/file-history directory.
func (claudeHome *Home) FileHistoryDir() string {
	return filepath.Join(claudeHome.Dir, "file-history")
}

// TodosDir returns the path to the ~/.claude/todos directory.
func (claudeHome *Home) TodosDir() string {
	return filepath.Join(claudeHome.Dir, "todos")
}

// UsageDataDir returns the path to the ~/.claude/usage-data directory.
func (claudeHome *Home) UsageDataDir() string {
	return filepath.Join(claudeHome.Dir, "usage-data")
}

// PluginsDataDir returns the path to the ~/.claude/plugins/data directory.
func (claudeHome *Home) PluginsDataDir() string {
	return filepath.Join(claudeHome.Dir, "plugins", "data")
}

// TasksDir returns the path to the ~/.claude/tasks directory.
func (claudeHome *Home) TasksDir() string {
	return filepath.Join(claudeHome.Dir, "tasks")
}
