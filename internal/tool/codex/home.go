package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/it-bens/cc-port/internal/tool"
)

// ErrProjectAbsenceUnresolved reports that a project was not found under
// this adapter's resolved Home.SQLiteDir while Codex may keep its state in a
// directory this adapter cannot establish: a discovered profile overlay
// declares a different sqlite_home, or a cloud bundle could not be checked
// (see projectAbsenceError). It is distinct from tool.ErrProjectAbsent,
// which means every source this adapter can check agrees the project is
// unknown: it does not match errors.Is(err, tool.ErrProjectAbsent), so
// move/export/stats sweep semantics treat it as a hard failure instead of
// silently skipping Codex the way a genuine absence would.
var ErrProjectAbsenceUnresolved = errors.New("project absence could not be established: the sqlite_home Codex uses is uncertain")

// configTOMLFileName is Codex's top-level configuration file, flat under the
// home directory (core/src/config/mod.rs:264, CONFIG_TOML_FILE).
const configTOMLFileName = "config.toml"

// sqliteHomeEnv is the environment variable Codex consults for the second
// tier of sqlite-home resolution (state/src/lib.rs:124, SQLITE_HOME_ENV).
const sqliteHomeEnv = "CODEX_SQLITE_HOME"

// Home is Codex's resolved state root for one Workspace: the primary
// directory, the resolved SQLite database directory (see resolveSQLiteDir),
// and the optional shared ~/.agents directory.
type Home struct {
	Dir       string
	SQLiteDir string
	AgentsDir string

	// sqliteSource records which resolution tier produced SQLiteDir. Its
	// zero value is the codex-home default, which is what a Home built
	// directly from Dir and SQLiteDir gets.
	sqliteSource sqliteHomeSource
	// resolutionWarnings name the machine-level sources that exist but could
	// not be checked while resolving SQLiteDir.
	resolutionWarnings []string
}

// sqliteHomeTier is one tier of Codex's sqlite-home resolution, lowest
// precedence first.
type sqliteHomeTier int

const (
	sqliteHomeFromCodexHome sqliteHomeTier = iota
	sqliteHomeFromEnvironment
	sqliteHomeFromMachineConfig
	sqliteHomeFromConfigTOML
	sqliteHomeFromManagedConfig
	sqliteHomeFromRequirement
)

// sqliteHomeSource names the tier that won and, for an explicit tier, where
// its value came from, so errors and warnings can point at it.
type sqliteHomeSource struct {
	tier sqliteHomeTier
	name string
}

// sqliteHomeResolution is resolveSQLiteDir's result.
type sqliteHomeResolution struct {
	dir      string
	source   sqliteHomeSource
	warnings []string
}

// newHome resolves sqliteDir and agentsDir for an already-validated dir.
// getenv is the injected environment-lookup seam (spec §1 construction
// seams): real Open calls pass os.Getenv, tests pass a fake so HOME and
// CODEX_SQLITE_HOME are controllable without mutating process-wide state.
func newHome(dir string, getenv func(string) string) (*Home, error) {
	sources, err := machineManagedSources(dir)
	if err != nil {
		return nil, err
	}
	resolution, err := resolveSQLiteDir(dir, getenv, &sources, time.Now())
	if err != nil {
		return nil, err
	}
	if err := requireExplicitSQLiteDir(resolution); err != nil {
		return nil, err
	}
	var agentsDir string
	if homeDir := getenv("HOME"); homeDir != "" {
		agentsDir = filepath.Join(homeDir, ".agents")
	}
	return &Home{
		Dir:                dir,
		SQLiteDir:          resolution.dir,
		AgentsDir:          agentsDir,
		sqliteSource:       resolution.source,
		resolutionWarnings: resolution.warnings,
	}, nil
}

// resolveSQLiteDir mirrors Codex's sqlite-home resolution. A managed
// requirement replaces the configured value outright
// (core/src/config/requirements.rs:35, 76-101, applied before resolution at
// core/src/config/mod.rs:3219-3224), and it also outranks
// $CODEX_SQLITE_HOME (core/src/config/requirements.rs:131-157). The
// configured value is the highest config layer that sets sqlite_home
// (config/src/config_layer_source.rs:33-51): managed config above
// config.toml, then config.toml, then the machine config below it.
// core/src/config/mod.rs:3996-4001 falls back to $CODEX_SQLITE_HOME, then
// the home directory itself.
func resolveSQLiteDir(dir string, getenv func(string) string, sources *managedSources, now time.Time) (sqliteHomeResolution, error) {
	managed, err := resolveManagedSQLiteHome(dir, sources, getenv("HOME"), now)
	if err != nil {
		return sqliteHomeResolution{}, err
	}
	resolution := sqliteHomeResolution{warnings: managed.warnings}
	userConfig, err := userConfigSQLiteHome(dir, getenv("HOME"))
	if err != nil {
		return sqliteHomeResolution{}, err
	}
	for _, candidate := range []struct {
		setting *sqliteHomeSetting
		tier    sqliteHomeTier
	}{
		{managed.requirement, sqliteHomeFromRequirement},
		{managed.managedConfig, sqliteHomeFromManagedConfig},
		{userConfig, sqliteHomeFromConfigTOML},
		{managed.machineConfig, sqliteHomeFromMachineConfig},
	} {
		if candidate.setting != nil {
			resolution.dir = candidate.setting.path
			resolution.source = sqliteHomeSource{tier: candidate.tier, name: candidate.setting.source}
			return resolution, nil
		}
	}

	// Codex trims the value, treats a blank one as unset, and resolves the
	// rest like any other AbsolutePathBuf against its working directory
	// (core/src/config/mod.rs:267-277, resolve_sqlite_home_env).
	if envValue := strings.TrimSpace(getenv(sqliteHomeEnv)); envValue != "" {
		cwd, err := os.Getwd()
		if err != nil {
			return sqliteHomeResolution{}, fmt.Errorf("resolve $%s: %w", sqliteHomeEnv, err)
		}
		resolved, err := resolveAgainstHome(cwd, envValue, getenv("HOME"))
		if err != nil {
			return sqliteHomeResolution{}, fmt.Errorf("resolve $%s: %w", sqliteHomeEnv, err)
		}
		resolution.dir = resolved
		resolution.source = sqliteHomeSource{tier: sqliteHomeFromEnvironment, name: "$" + sqliteHomeEnv}
		return resolution, nil
	}
	resolution.dir = dir
	return resolution, nil
}

// userConfigSQLiteHome reads sqlite_home from config.toml; a relative value
// resolves against the codex home.
func userConfigSQLiteHome(dir, osHome string) (*sqliteHomeSetting, error) {
	configPath := filepath.Join(dir, configTOMLFileName)
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path constructed from the resolved codex home
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	return sqliteHomeFromTOML(data, "sqlite_home in "+configPath, dir, osHome)
}

// requireExplicitSQLiteDir refuses an explicitly chosen sqlite_home that is
// not an existing directory. Database discovery reads a missing directory as
// "no databases", so without this a mistyped sqlite_home would report every
// project as unknown to Codex. The codex-home default is exempt: Open already
// requires that directory to exist.
func requireExplicitSQLiteDir(resolution sqliteHomeResolution) error {
	if resolution.source.tier == sqliteHomeFromCodexHome {
		return nil
	}
	info, err := os.Stat(resolution.dir)
	if err != nil {
		return fmt.Errorf("sqlite_home %s from %s: %w", resolution.dir, resolution.source.name, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("sqlite_home %s from %s is not a directory", resolution.dir, resolution.source.name)
	}
	return nil
}

// sqliteHomeWarnings returns the caveats on home.SQLiteDir that move
// (ResidualWarnings), export, and import (Finalize) report: the machine-level
// sources recorded as unchecked while resolving it, then any profile overlay
// whose sqlite_home diverges from it (see profileSQLiteHomeDivergence).
func sqliteHomeWarnings(home *Home, getenv func(string) string) ([]string, error) {
	divergence, err := profileSQLiteHomeDivergence(home, getenv)
	if err != nil {
		return nil, err
	}
	warnings := append([]string(nil), home.resolutionWarnings...)
	if divergence != "" {
		warnings = append(warnings, divergence)
	}
	return warnings, nil
}

// profileSQLiteHomeDivergence inspects every discovered profile overlay
// (<profile>.config.toml) for a sqlite_home declaration that resolves to a
// directory other than home.SQLiteDir. Codex's profile-v2 selection
// (the --profile CLI flag) is a runtime argument, never recorded in
// config.toml: core/src/config/mod.rs:3319-3326 refuses to start Codex at
// all when a legacy `profile` key is even present in config.toml, so there
// is no on-disk record of which profile, if any, was active for the
// sessions currently on disk. resolveSQLiteDir therefore always resolves
// against base config.toml, matching Codex's own behavior with no
// --profile flag; this reports rather than silently trusting that
// resolution whenever a profile overlay declares a sqlite_home that
// disagrees with it. A managed requirement overrides every overlay's
// sqlite_home (core/src/config/requirements.rs:35), and managed config
// layers outrank profiles (config/src/config_layer_source.rs:33-51), so no
// overlay can diverge once either set SQLiteDir.
func profileSQLiteHomeDivergence(home *Home, getenv func(string) string) (string, error) {
	if home.sqliteSource.tier >= sqliteHomeFromManagedConfig {
		return "", nil
	}
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
		setting, err := sqliteHomeFromTOML(data, path, home.Dir, getenv("HOME"))
		if err != nil {
			return "", err
		}
		if setting == nil {
			continue
		}
		if setting.path != home.SQLiteDir {
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
// Reporting a confident tool.ErrProjectAbsent derived from Home.SQLiteDir is
// a best-guess answer presented as fact whenever Codex may keep the
// project's state somewhere else: a profile overlay declaring a different
// sqlite_home, or a cloud bundle cc-port could not check. Fail-hard forbids
// that, so this returns ErrProjectAbsenceUnresolved carrying every
// sqliteHomeWarnings entry instead. With no such caveat it returns the
// ordinary tool.ErrProjectAbsent.
func (workspace *Workspace) projectAbsenceError() error {
	warnings, err := sqliteHomeWarnings(workspace.home, workspace.getenv)
	if err != nil {
		return err
	}
	if len(warnings) == 0 {
		return tool.ErrProjectAbsent
	}
	return fmt.Errorf(
		"%w: not found in the resolved sqlite directory %s; %s",
		ErrProjectAbsenceUnresolved, workspace.home.SQLiteDir, strings.Join(warnings, "; "),
	)
}

// resolveAgainstHome mirrors AbsolutePathBuf::resolve_path_against_base
// (utils/absolute-path/src/lib.rs:45-56): expand a leading ~, join a
// relative value onto base, and normalize. An empty value therefore resolves
// to base itself.
func resolveAgainstHome(base, path, osHome string) (string, error) {
	path = expandHomeDirectory(path, osHome)
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Abs(filepath.Join(base, path))
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
