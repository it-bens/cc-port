package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

func fakeGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func noProcesses() ([]ProcessInfo, error) { return nil, nil }

func TestDetectReportsAbsentWhenDefaultHomeMissing(t *testing.T) {
	homeDir := t.TempDir()
	adapter := NewAdapter(fakeGetenv(map[string]string{"HOME": homeDir}), noProcesses)

	detected, err := adapter.Detect()

	require.NoError(t, err)
	assert.False(t, detected)
}

func TestDetectReportsPresentWhenDefaultHomeExists(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".codex"), 0o750))
	adapter := NewAdapter(fakeGetenv(map[string]string{"HOME": homeDir}), noProcesses)

	detected, err := adapter.Detect()

	require.NoError(t, err)
	assert.True(t, detected)
}

func TestOpenDefaultLocationReportsToolAbsent(t *testing.T) {
	homeDir := t.TempDir()
	adapter := NewAdapter(fakeGetenv(map[string]string{"HOME": homeDir}), noProcesses)

	_, err := adapter.Open("")

	assert.ErrorIs(t, err, tool.ErrToolAbsent)
}

func TestOpenExplicitOverrideMustExist(t *testing.T) {
	homeDir := t.TempDir()
	adapter := NewAdapter(fakeGetenv(map[string]string{"HOME": homeDir}), noProcesses)

	_, err := adapter.Open(filepath.Join(homeDir, "missing-codex-home"))

	require.Error(t, err)
	assert.NotErrorIs(t, err, tool.ErrToolAbsent, "an explicit override that does not exist must fail hard, not report absence")
}

func TestOpenExplicitOverrideRejectsFile(t *testing.T) {
	homeDir := t.TempDir()
	filePath := filepath.Join(homeDir, "not-a-dir")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0o600))
	adapter := NewAdapter(fakeGetenv(map[string]string{"HOME": homeDir}), noProcesses)

	_, err := adapter.Open(filePath)

	require.Error(t, err)
}

func TestOpenExplicitOverrideCanonicalizesSymlink(t *testing.T) {
	homeDir := t.TempDir()
	realDir := filepath.Join(homeDir, "real-codex")
	require.NoError(t, os.MkdirAll(realDir, 0o750))
	linkDir := filepath.Join(homeDir, "link-codex")
	require.NoError(t, os.Symlink(realDir, linkDir))
	adapter := NewAdapter(fakeGetenv(map[string]string{"HOME": homeDir}), noProcesses)

	workspace, err := adapter.Open(linkDir)

	require.NoError(t, err)
	resolvedReal, err := filepath.EvalSymlinks(realDir)
	require.NoError(t, err)
	assert.Equal(t, resolvedReal, workspace.Root())
}

func TestResolveSQLiteDirDefaultsToCodexHome(t *testing.T) {
	dir := t.TempDir()

	resolution, err := resolveSQLiteDir(dir, fakeGetenv(nil), absentManagedSources(t, dir), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, dir, resolution.dir)
}

func TestResolveSQLiteDirUsesEnvOverCodexHome(t *testing.T) {
	dir := t.TempDir()
	sqliteHome := filepath.Join(dir, "sqlite-elsewhere")
	getenv := fakeGetenv(map[string]string{"CODEX_SQLITE_HOME": sqliteHome})

	resolution, err := resolveSQLiteDir(dir, getenv, absentManagedSources(t, dir), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, sqliteHome, resolution.dir)
}

func TestResolveSQLiteDirPrefersConfigTOMLOverEnv(t *testing.T) {
	dir := t.TempDir()
	configured := filepath.Join(dir, "sqlite-from-config")
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, configTOMLFileName),
		[]byte(`sqlite_home = "`+configured+`"`+"\n"),
		0o600,
	))
	getenv := fakeGetenv(map[string]string{"CODEX_SQLITE_HOME": filepath.Join(dir, "ignored")})

	resolution, err := resolveSQLiteDir(dir, getenv, absentManagedSources(t, dir), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, configured, resolution.dir)
}

func TestResolveSQLiteDirResolvesRelativeConfigValueAgainstCodexHome(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, configTOMLFileName), []byte("sqlite_home = \"relative-sqlite\"\n"), 0o600))

	resolution, err := resolveSQLiteDir(dir, fakeGetenv(nil), absentManagedSources(t, dir), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "relative-sqlite"), resolution.dir)
}

func TestResolveSQLiteDirExpandsTildeConfigValueAgainstOSHome(t *testing.T) {
	dir := t.TempDir()
	osHome := filepath.Join(t.TempDir(), "os-home")
	require.NoError(t, os.WriteFile(filepath.Join(dir, configTOMLFileName), []byte("sqlite_home = \"~/state\"\n"), 0o600))

	resolution, err := resolveSQLiteDir(dir, fakeGetenv(map[string]string{"HOME": osHome}), absentManagedSources(t, dir), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(osHome, "state"), resolution.dir)
}

func TestResolveSQLiteDirTreatsBlankEnvironmentValueAsUnset(t *testing.T) {
	dir := t.TempDir()
	getenv := fakeGetenv(map[string]string{sqliteHomeEnv: "  \t"})

	resolution, err := resolveSQLiteDir(dir, getenv, absentManagedSources(t, dir), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, dir, resolution.dir)
	assert.Equal(t, sqliteHomeFromCodexHome, resolution.source.tier)
}

func TestResolveSQLiteDirTrimsEnvironmentValue(t *testing.T) {
	dir := t.TempDir()
	sqliteHome := filepath.Join(dir, "sqlite-elsewhere")
	getenv := fakeGetenv(map[string]string{sqliteHomeEnv: " " + sqliteHome + "\n"})

	resolution, err := resolveSQLiteDir(dir, getenv, absentManagedSources(t, dir), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, sqliteHome, resolution.dir)
}

func TestResolveSQLiteDirResolvesRelativeEnvironmentValueAgainstProcessCWD(t *testing.T) {
	dir := t.TempDir()
	currentDir, err := os.Getwd()
	require.NoError(t, err)
	getenv := fakeGetenv(map[string]string{sqliteHomeEnv: "relative-sqlite"})

	resolution, err := resolveSQLiteDir(dir, getenv, absentManagedSources(t, dir), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(currentDir, "relative-sqlite"), resolution.dir)
}

func TestResolveSQLiteDirExpandsTildeEnvironmentValueAgainstOSHome(t *testing.T) {
	dir := t.TempDir()
	osHome := filepath.Join(t.TempDir(), "os-home")
	getenv := fakeGetenv(map[string]string{"HOME": osHome, sqliteHomeEnv: "~/state"})

	resolution, err := resolveSQLiteDir(dir, getenv, absentManagedSources(t, dir), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(osHome, "state"), resolution.dir)
}

func TestResolveSQLiteDirResolvesEmptyConfigValueToCodexHome(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, configTOMLFileName), []byte(`sqlite_home = ""`), 0o600))
	getenv := fakeGetenv(map[string]string{sqliteHomeEnv: filepath.Join(dir, "ignored")})

	resolution, err := resolveSQLiteDir(dir, getenv, absentManagedSources(t, dir), fixtureNow)

	require.NoError(t, err)
	assert.Equal(t, dir, resolution.dir)
	assert.Equal(t, sqliteHomeFromConfigTOML, resolution.source.tier)
}

func TestProfileSQLiteHomeDivergence_EmptyWhenNoOverlayDeclaresSQLiteHome(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "work.config.toml"),
		[]byte("[projects.\"/Users/fixture/project\"]\ntrust_level = \"trusted\"\n"),
		0o600,
	))
	home := &Home{Dir: dir, SQLiteDir: dir}

	warning, err := profileSQLiteHomeDivergence(home, fakeGetenv(nil))

	require.NoError(t, err)
	assert.Empty(t, warning)
}

// TestProfileSQLiteHomeDivergence_ReportsDivergentOverlay guards the fail-loud
// path for finding H2: Codex's active --profile selection is a runtime CLI
// argument never recorded in config.toml (core/src/config/mod.rs:3319-3326
// refuses to start Codex at all when a legacy `profile` key is present), so
// resolveSQLiteDir can never determine which profile, if any, was active
// for the state currently on disk and always resolves against base
// config.toml. When a discovered profile overlay declares a sqlite_home
// different from that resolution, cc-port must say so rather than silently
// trusting the base resolution.
func TestProfileSQLiteHomeDivergence_ReportsDivergentOverlay(t *testing.T) {
	dir := t.TempDir()
	elsewhere := filepath.Join(dir, "elsewhere")
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "work.config.toml"),
		[]byte("sqlite_home = \""+elsewhere+"\"\n"),
		0o600,
	))
	home := &Home{Dir: dir, SQLiteDir: dir}

	warning, err := profileSQLiteHomeDivergence(home, fakeGetenv(nil))

	require.NoError(t, err)
	assert.Contains(t, warning, "work.config.toml")
	assert.Contains(t, warning, dir)
}

func TestOpenPopulatesAgentsDirFromHome(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".codex"), 0o750))
	adapter := NewAdapter(fakeGetenv(map[string]string{"HOME": homeDir}), noProcesses)

	workspaceIface, err := adapter.Open("")
	require.NoError(t, err)
	workspace, ok := workspaceIface.(*Workspace)
	require.True(t, ok)

	assert.Equal(t, filepath.Join(homeDir, ".agents"), workspace.home.AgentsDir)
}
