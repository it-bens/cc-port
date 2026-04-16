package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EncodePath converts an absolute filesystem path to the directory name format
// used by Claude Code under ~/.claude/projects/.
//
// The encoding is lossy: "/", ".", and " " all become "-", and a "-" is prepended.
// A literal "-" in the original path is indistinguishable from these replacements.
// The original path cannot be reliably recovered from the encoded form.
func EncodePath(absPath string) string {
	replacer := strings.NewReplacer(
		"/", "-",
		".", "-",
		" ", "-",
	)
	return replacer.Replace(absPath)
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
