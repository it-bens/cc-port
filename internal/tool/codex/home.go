package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// configTOMLFileName is Codex's top-level configuration file, flat under the
// home directory (core/src/config/mod.rs:272, CONFIG_TOML_FILE).
const configTOMLFileName = "config.toml"

// sqliteHomeEnv is the environment variable Codex consults for the second
// tier of sqlite-home resolution (state/src/lib.rs:95, SQLITE_HOME_ENV).
const sqliteHomeEnv = "CODEX_SQLITE_HOME"

// Home is Codex's resolved state root for one Workspace: the primary
// directory, the resolved SQLite database directory (three-tier
// resolution, see resolveSQLiteDir), and the optional shared ~/.agents
// directory.
type Home struct {
	Dir       string
	SQLiteDir string
	AgentsDir string
}

// newHome resolves sqliteDir and agentsDir for an already-validated dir.
// getenv is the injected environment-lookup seam (spec §1 construction
// seams): real Open calls pass os.Getenv, tests pass a fake so HOME and
// CODEX_SQLITE_HOME are controllable without mutating process-wide state.
func newHome(dir string, getenv func(string) string) (*Home, error) {
	sqliteDir, err := resolveSQLiteDir(dir, getenv)
	if err != nil {
		return nil, err
	}
	var agentsDir string
	if homeDir := getenv("HOME"); homeDir != "" {
		agentsDir = filepath.Join(homeDir, ".agents")
	}
	return &Home{Dir: dir, SQLiteDir: sqliteDir, AgentsDir: agentsDir}, nil
}

// resolveSQLiteDir mirrors Codex's three-tier sqlite-home resolution
// (core/src/config/mod.rs:3669-3674): the sqlite_home key in config.toml,
// then $CODEX_SQLITE_HOME, then the home directory itself.
func resolveSQLiteDir(dir string, getenv func(string) string) (string, error) {
	configPath := filepath.Join(dir, configTOMLFileName)
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path constructed from the resolved codex home
	switch {
	case err == nil:
		var probe struct {
			SQLiteHome string `toml:"sqlite_home"`
		}
		if unmarshalErr := toml.Unmarshal(data, &probe); unmarshalErr != nil {
			return "", fmt.Errorf("parse %s for sqlite_home: %w", configPath, unmarshalErr)
		}
		if probe.SQLiteHome != "" {
			return resolveAgainstHome(dir, probe.SQLiteHome, getenv("HOME"))
		}
	case errors.Is(err, os.ErrNotExist):
		// No config.toml yet: fall through to the environment tier.
	default:
		return "", fmt.Errorf("read %s: %w", configPath, err)
	}

	if envValue := getenv(sqliteHomeEnv); envValue != "" {
		return resolveAgainstCWD(envValue)
	}
	return dir, nil
}

func resolveAgainstHome(home, path, osHome string) (string, error) {
	path = expandHomeDirectory(path, osHome)
	if filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Abs(filepath.Join(home, path))
}

// expandHomeDirectory mirrors AbsolutePathBuf: only ~ and ~/... expand, and
// the expansion happens before resolving a relative value against its base.
func expandHomeDirectory(path, home string) string {
	if home == "" || path == "~" {
		if path == "~" && home != "" {
			return home
		}
		return path
	}
	if len(path) >= 2 && path[0] == '~' && path[1] == '/' {
		return filepath.Join(home, path[2:])
	}
	return path
}

// resolveAgainstCWD makes path absolute against the current process's
// working directory, matching Codex's own resolve_sqlite_home_env
// behavior for a relative $CODEX_SQLITE_HOME (core/src/config/mod.rs:281-286).
func resolveAgainstCWD(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("absolute path for %q: %w", path, err)
	}
	return absPath, nil
}

// canonicalizeExistingDir validates that path exists, is a directory, and
// returns its symlink-resolved absolute form. Used for an explicit
// --codex-home override, which (unlike Claude's lazily created home) must
// already exist.
func canonicalizeExistingDir(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("absolute path for %q: %w", path, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", absPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", absPath)
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks for %q: %w", absPath, err)
	}
	return resolved, nil
}

// defaultCodexHome returns $HOME/.codex, unresolved and possibly
// non-existent — Detect and the default-location Open path decide
// separately whether that absence is fatal.
func defaultCodexHome(getenv func(string) string) (string, error) {
	homeDir := getenv("HOME")
	if homeDir == "" {
		return "", fmt.Errorf("determine home directory: $HOME is unset")
	}
	return filepath.Join(homeDir, ".codex"), nil
}
