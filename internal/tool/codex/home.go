package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/it-bens/cc-port/internal/tool"
)

// ErrProjectAbsenceUnresolved reports that a project was not found under
// this adapter's base-resolved Home.SQLiteDir while a discovered profile
// overlay declares a different sqlite_home this adapter has no way to
// resolve against (see profileSQLiteHomeWarning). It is distinct from
// tool.ErrProjectAbsent, which means every source this adapter can check
// agrees the project is unknown: it does not match
// errors.Is(err, tool.ErrProjectAbsent), so move/export/stats sweep
// semantics correctly treat it as a hard failure instead of silently
// skipping Codex the way a genuine absence would.
var ErrProjectAbsenceUnresolved = errors.New("project absence could not be established: a profile overlay declares a divergent sqlite_home")

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

// profileSQLiteHomeWarning inspects every discovered profile overlay
// (<profile>.config.toml) for a sqlite_home declaration that resolves to a
// directory other than home.SQLiteDir. Codex's profile-v2 selection
// (the --profile CLI flag) is a runtime argument, never recorded in
// config.toml: core/src/config/mod.rs:3047-3054 refuses to start Codex at
// all when a legacy `profile` key is even present in config.toml, so there
// is no on-disk record of which profile, if any, was active for the
// sessions currently on disk. resolveSQLiteDir therefore always resolves
// against base config.toml, matching Codex's own behavior with no
// --profile flag; this warns rather than silently trusting that
// resolution whenever a profile overlay declares a sqlite_home that
// disagrees with it. On its own this only warns a known project's state
// may be incomplete; projectAbsenceError uses the same check to stop an
// unknown project from being reported as flatly absent.
func profileSQLiteHomeWarning(home *Home, getenv func(string) string) (string, error) {
	files, err := discoverConfigTOMLFiles(home)
	if err != nil {
		return "", err
	}
	var divergent []string
	for _, path := range files {
		if filepath.Base(path) == configTOMLFileName {
			continue
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: path from adapter-controlled config discovery
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		var probe struct {
			SQLiteHome string `toml:"sqlite_home"`
		}
		if unmarshalErr := toml.Unmarshal(data, &probe); unmarshalErr != nil {
			return "", fmt.Errorf("parse %s for sqlite_home: %w", path, unmarshalErr)
		}
		if probe.SQLiteHome == "" {
			continue
		}
		resolved, err := resolveAgainstHome(home.Dir, probe.SQLiteHome, getenv("HOME"))
		if err != nil {
			return "", fmt.Errorf("resolve sqlite_home in %s: %w", path, err)
		}
		if resolved != home.SQLiteDir {
			divergent = append(divergent, filepath.Base(path))
		}
	}
	if len(divergent) == 0 {
		return "", nil
	}
	sort.Strings(divergent)
	return fmt.Sprintf(
		"%s declare(s) a sqlite_home different from the resolved %s; Codex's active --profile is a runtime flag not "+
			"recorded on disk, so cc-port cannot determine which is authoritative and inspects only the base config.toml resolution",
		strings.Join(divergent, ", "), home.SQLiteDir,
	), nil
}

// projectAbsenceError decides what "not found under every source this
// adapter checks" means once knowsProject or projectKnown reports false.
// Reporting a confident tool.ErrProjectAbsent derived only from the
// base-resolved SQLiteDir is a best-guess answer presented as fact
// whenever a profile overlay might hold the project's real state under a
// directory this adapter never looked in; fail-hard forbids that, so this
// returns ErrProjectAbsenceUnresolved instead whenever a divergent overlay
// exists. When no overlay diverges, the overwhelmingly common case, this
// returns the ordinary tool.ErrProjectAbsent and every caller's behavior
// is unchanged.
func (workspace *Workspace) projectAbsenceError() error {
	warning, err := profileSQLiteHomeWarning(workspace.home, workspace.getenv)
	if err != nil {
		return err
	}
	if warning == "" {
		return tool.ErrProjectAbsent
	}
	return fmt.Errorf(
		"%w: not found in the base-resolved sqlite directory %s; %s",
		ErrProjectAbsenceUnresolved, workspace.home.SQLiteDir, warning,
	)
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
