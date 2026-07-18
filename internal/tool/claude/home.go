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
	return newHome(override, os.Getenv)
}

func newHome(override string, getenv func(string) string) (*Home, error) {
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

	homeDir := getenv("HOME")
	if homeDir == "" {
		return nil, fmt.Errorf("determine home directory: $HOME is unset")
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

// PluginsInstalledFile returns the path to the ~/.claude/plugins/installed_plugins.json file.
func (claudeHome *Home) PluginsInstalledFile() string {
	return filepath.Join(claudeHome.Dir, "plugins", "installed_plugins.json")
}

// KnownMarketplacesFile returns the path to the ~/.claude/plugins/known_marketplaces.json file.
func (claudeHome *Home) KnownMarketplacesFile() string {
	return filepath.Join(claudeHome.Dir, "plugins", "known_marketplaces.json")
}

// homeAnchor returns the current machine's real user home directory,
// resolved through any symlink, for use as the {{HOME}} placeholder anchor.
// A symlinked HOME must resolve to its target before an anchor filter
// compares it against project paths, otherwise every home-rooted candidate
// is silently dropped. Rejects "/" and non-absolute values so the anchor
// cannot match every absolute path in the corpus.
func homeAnchor(getenv func(string) string) (string, error) {
	homePath := getenv("HOME")
	if homePath == "" {
		return "", fmt.Errorf("determine home directory: $HOME is unset")
	}
	if !filepath.IsAbs(homePath) {
		return "", fmt.Errorf("invalid home directory %q: must be absolute", homePath)
	}
	resolved, err := fsutil.ResolveExistingAncestor(homePath)
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	cleaned := filepath.Clean(resolved)
	if !filepath.IsAbs(cleaned) || cleaned == "/" {
		return "", fmt.Errorf("invalid home directory %q", cleaned)
	}
	return cleaned, nil
}
