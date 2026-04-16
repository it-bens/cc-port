package claude

import (
	"errors"
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
//
// Callers must pass a fully symlink-resolved path — Claude Code stores projects
// under the resolved path's encoded form (e.g. /tmp/foo on macOS is written as
// -private-tmp-foo because /tmp links to /private/tmp). Use ResolveProjectPath
// on any path that came from user input before encoding.
func EncodePath(absPath string) string {
	replacer := strings.NewReplacer(
		"/", "-",
		".", "-",
		" ", "-",
	)
	return replacer.Replace(absPath)
}

// ResolveProjectPath normalizes a user-supplied project path so its encoded
// form matches what Claude Code writes on disk. It converts the path to
// absolute form, then resolves symlinks on the longest existing prefix. Any
// trailing components that do not yet exist are preserved unchanged.
//
// This matters because Claude Code resolves symlinks through the filesystem
// before encoding — on macOS, a project started under /tmp/foo is stored as
// -private-tmp-foo (/tmp links to /private/tmp). Without this step,
// `cc-port move /tmp/foo ...` would look for -tmp-foo and report "project
// directory not found".
//
// macOS and Linux only. Windows path semantics are not handled.
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
