package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

func newWitnessWorkspace(t *testing.T, listProcesses ProcessLister) *Workspace {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dotcodex")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	home := &Home{Dir: dir, SQLiteDir: dir}
	return newWorkspace(home, fakeGetenv(nil), listProcesses)
}

func TestActiveWritersEmptyOnFreshHome(t *testing.T) {
	workspace := newWitnessWorkspace(t, noProcesses)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.Empty(t, active)
}

func TestActiveWritersDetectsCodexProcess(t *testing.T) {
	lister := func() ([]ProcessInfo, error) {
		return []ProcessInfo{{PID: 4242, Name: "codex"}}, nil
	}
	workspace := newWitnessWorkspace(t, lister)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, 4242, active[0].Pid)
}

func TestActiveWritersDetectsCodexProcessByBasenameOfFullPath(t *testing.T) {
	lister := func() ([]ProcessInfo, error) {
		return []ProcessInfo{{PID: 4242, Name: "/Users/fixture/.local/bin/codex"}}, nil
	}
	workspace := newWitnessWorkspace(t, lister)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	require.Len(t, active, 1)
}

func TestActiveWritersIgnoresUnrelatedProcess(t *testing.T) {
	lister := func() ([]ProcessInfo, error) {
		return []ProcessInfo{{PID: 4242, Name: "vim"}}, nil
	}
	workspace := newWitnessWorkspace(t, lister)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.Empty(t, active)
}

func TestActiveWritersWrapsErrNoWitnessWhenProcessListFails(t *testing.T) {
	lister := func() ([]ProcessInfo, error) { return nil, assert.AnError }
	workspace := newWitnessWorkspace(t, lister)

	_, err := workspace.ActiveWriters()

	require.Error(t, err)
	assert.ErrorIs(t, err, tool.ErrNoWitness)
}

func TestActiveWritersWrapsErrNoWitnessWhenBusyProbeCannotDiscoverDatabases(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := t.TempDir()
	sqliteDir := filepath.Join(dir, "sqlite")
	require.NoError(t, os.MkdirAll(sqliteDir, 0o750))
	require.NoError(t, os.Chmod(sqliteDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sqliteDir, 0o700) }) //nolint:gosec // G302: cleanup restores test directory access
	home := &Home{Dir: dir, SQLiteDir: sqliteDir}
	workspace := newWorkspace(home, fakeGetenv(nil), noProcesses)

	_, err := workspace.ActiveWriters()

	assert.ErrorIs(t, err, tool.ErrNoWitness)
}

func TestActiveWritersDetectsBusyDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dotcodex")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	dbPath := filepath.Join(dir, "state_5.sqlite")
	buildFixtureStateDB(t, dbPath)

	blocker, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer func() { _ = blocker.Close() }()
	_, err = blocker.ExecContext(context.Background(), "BEGIN IMMEDIATE")
	require.NoError(t, err)
	defer func() { _, _ = blocker.ExecContext(context.Background(), "ROLLBACK") }()

	home := &Home{Dir: dir, SQLiteDir: dir}
	workspace := newWorkspace(home, fakeGetenv(nil), noProcesses)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.NotEmpty(t, active)
}
