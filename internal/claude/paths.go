package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/it-bens/cc-port/internal/fsutil"
)

// EncodePath encodes an absolute, symlink-resolved path into the directory-name
// form Claude Code uses under ~/.claude/projects/. Pass pre-resolved paths or
// the result will not match what Claude Code wrote.
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
	return fsutil.ResolveExistingAncestor(absPath)
}

// Home represents the root of Claude Code's data storage.
type Home struct {
	Dir        string // Path to the ~/.claude directory.
	ConfigFile string // Path to the ~/.claude.json file.
}

// NewHome creates a Home. If override is empty, it uses the default
// locations derived from the user's home directory. If override is
// provided, it is normalised to an absolute path via filepath.Abs and
// used as the Dir; ConfigFile is derived as Dir + ".json".
//
// Normalisation is required because downstream staging preflight
// (internal/importer/importer.go:stagingTempPath) feeds
// filepath.Dir(<derived path>) into fsutil.ResolveExistingAncestor,
// which panics on relative input. Converting operational input here
// keeps the panic path reserved for real programmer errors.
func NewHome(override string) (*Home, error) {
	if override != "" {
		absOverride, err := filepath.Abs(override)
		if err != nil {
			return nil, fmt.Errorf("absolute path for claude home override %q: %w", override, err)
		}
		return &Home{
			Dir:        absOverride,
			ConfigFile: absOverride + ".json",
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
