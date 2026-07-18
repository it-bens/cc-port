package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

var fixedWitnessNow = time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)

func newWitnessWorkspace(t *testing.T, listProcesses ProcessLister, now func() time.Time, pidAlive func(int) bool) *Workspace {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dotcodex")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	home := &Home{Dir: dir, SQLiteDir: dir}
	return newWorkspace(home, fakeGetenv(nil), listProcesses, now, pidAlive, DefaultTranscodeCaps())
}

func TestActiveWritersEmptyOnFreshHome(t *testing.T) {
	workspace := newWitnessWorkspace(t, noProcesses, func() time.Time { return fixedWitnessNow }, func(int) bool { return false })

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.Empty(t, active)
}

func TestActiveWritersDetectsCodexProcess(t *testing.T) {
	lister := func() ([]ProcessInfo, error) {
		return []ProcessInfo{{PID: 4242, Name: "codex"}}, nil
	}
	workspace := newWitnessWorkspace(t, lister, func() time.Time { return fixedWitnessNow }, func(int) bool { return false })

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, 4242, active[0].Pid)
}

func TestActiveWritersDetectsCodexProcessByBasenameOfFullPath(t *testing.T) {
	lister := func() ([]ProcessInfo, error) {
		return []ProcessInfo{{PID: 4242, Name: "/Users/fixture/.local/bin/codex"}}, nil
	}
	workspace := newWitnessWorkspace(t, lister, func() time.Time { return fixedWitnessNow }, func(int) bool { return false })

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	require.Len(t, active, 1)
}

func TestActiveWritersIgnoresUnrelatedProcess(t *testing.T) {
	lister := func() ([]ProcessInfo, error) {
		return []ProcessInfo{{PID: 4242, Name: "vim"}}, nil
	}
	workspace := newWitnessWorkspace(t, lister, func() time.Time { return fixedWitnessNow }, func(int) bool { return false })

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.Empty(t, active)
}

func TestActiveWritersWrapsErrNoWitnessWhenProcessListFails(t *testing.T) {
	lister := func() ([]ProcessInfo, error) { return nil, assert.AnError }
	workspace := newWitnessWorkspace(t, lister, func() time.Time { return fixedWitnessNow }, func(int) bool { return false })

	_, err := workspace.ActiveWriters()

	require.Error(t, err)
	assert.ErrorIs(t, err, tool.ErrNoWitness)
}

func TestActiveWritersWrapsErrNoWitnessWhenSQLiteDirectoryIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := t.TempDir()
	sqliteDir := filepath.Join(dir, "sqlite")
	require.NoError(t, os.MkdirAll(sqliteDir, 0o750))
	require.NoError(t, os.Chmod(sqliteDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sqliteDir, 0o700) }) //nolint:gosec // G302: cleanup restores test directory access
	home := &Home{Dir: dir, SQLiteDir: sqliteDir}
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedWitnessNow }, func(int) bool { return false }, DefaultTranscodeCaps(),
	)

	_, err := workspace.ActiveWriters()

	assert.ErrorIs(t, err, tool.ErrNoWitness)
}

func TestActiveWritersDetectsFreshRolloutFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dotcodex")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, sessionsSubdir), 0o750))
	rolloutPath := filepath.Join(dir, sessionsSubdir, "rollout-fixture.jsonl")
	require.NoError(t, os.WriteFile(rolloutPath, []byte("{}\n"), 0o600))

	fixedNow := fixedWitnessNow
	require.NoError(t, os.Chtimes(rolloutPath, fixedNow, fixedNow))
	home := &Home{Dir: dir, SQLiteDir: dir}
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedNow }, func(int) bool { return false }, DefaultTranscodeCaps(),
	)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.NotEmpty(t, active, "a rollout modified just now must count as fresh")
}

func TestActiveWritersIgnoresStaleRolloutFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dotcodex")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, sessionsSubdir), 0o750))
	rolloutPath := filepath.Join(dir, sessionsSubdir, "rollout-fixture.jsonl")
	require.NoError(t, os.WriteFile(rolloutPath, []byte("{}\n"), 0o600))
	old := fixedWitnessNow.Add(-1 * time.Hour)
	require.NoError(t, os.Chtimes(rolloutPath, old, old))

	home := &Home{Dir: dir, SQLiteDir: dir}
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedWitnessNow }, func(int) bool { return false }, DefaultTranscodeCaps(),
	)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.Empty(t, active)
}

func writeDaemonPIDFile(t *testing.T, dir string, pid int) {
	t.Helper()
	daemonDir := filepath.Join(dir, appServerDaemonSubdir)
	require.NoError(t, os.MkdirAll(daemonDir, 0o750))
	data, err := json.Marshal(pidRecord{PID: pid})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(daemonDir, appServerPIDFileName), data, 0o600))
}

func TestActiveWritersDetectsLiveDaemonPID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dotcodex")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	writeDaemonPIDFile(t, dir, 4242)

	home := &Home{Dir: dir, SQLiteDir: dir}
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedWitnessNow }, func(pid int) bool { return pid == 4242 }, DefaultTranscodeCaps(),
	)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.NotEmpty(t, active)
}

func TestActiveWritersIgnoresDeadDaemonPID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dotcodex")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	writeDaemonPIDFile(t, dir, 4242)

	home := &Home{Dir: dir, SQLiteDir: dir}
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedWitnessNow }, func(int) bool { return false }, DefaultTranscodeCaps(),
	)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.Empty(t, active)
}

func TestActiveWritersBlocksOnMalformedDaemonPIDFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dotcodex")
	daemonDir := filepath.Join(dir, appServerDaemonSubdir)
	require.NoError(t, os.MkdirAll(daemonDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(daemonDir, appServerPIDFileName), []byte("not-json"), 0o600))
	home := &Home{Dir: dir, SQLiteDir: dir}
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedWitnessNow }, func(int) bool { return false }, DefaultTranscodeCaps(),
	)

	_, err := workspace.ActiveWriters()

	assert.ErrorIs(t, err, tool.ErrNoWitness)
}

func TestActiveWritersDetectsHeldDaemonLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dotcodex")
	daemonDir := filepath.Join(dir, appServerDaemonSubdir)
	require.NoError(t, os.MkdirAll(daemonDir, 0o750))
	lockPath := filepath.Join(daemonDir, daemonLockFileName)
	require.NoError(t, os.WriteFile(lockPath, nil, 0o600))

	// A separate flock.Flock on the same path simulates another process
	// holding the lock: flock(2) locks are per open-file-description, so
	// this conflicts with the witness's own TryLock even from the same
	// test process.
	externalHolder := flock.New(lockPath)
	ok, err := externalHolder.TryLock()
	require.NoError(t, err)
	require.True(t, ok)
	defer func() { _ = externalHolder.Unlock() }()

	home := &Home{Dir: dir, SQLiteDir: dir}
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedWitnessNow }, func(int) bool { return false }, DefaultTranscodeCaps(),
	)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.NotEmpty(t, active)
}

func TestActiveWritersIgnoresFreshSuccessfulCompressionMarker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dotcodex")
	tmpDir := filepath.Join(dir, tmpSubdir)
	require.NoError(t, os.MkdirAll(tmpDir, 0o750))
	markerPath := filepath.Join(tmpDir, compressionLockFileName)
	require.NoError(t, os.WriteFile(markerPath, []byte("pid=4242 started_at=fixture\n"), 0o600))
	require.NoError(t, os.Chtimes(markerPath, fixedWitnessNow, fixedWitnessNow))

	home := &Home{Dir: dir, SQLiteDir: dir}
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedWitnessNow }, func(int) bool { return false }, DefaultTranscodeCaps(),
	)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.Empty(t, active, "a marker with a dead pid must not count, even when fresh")
}

func TestActiveWritersDetectsFreshCompressionMarkerWithLivePID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dotcodex")
	tmpDir := filepath.Join(dir, tmpSubdir)
	require.NoError(t, os.MkdirAll(tmpDir, 0o750))
	markerPath := filepath.Join(tmpDir, compressionLockFileName)
	require.NoError(t, os.WriteFile(markerPath, []byte("pid=4242 started_at=fixture\n"), 0o600))
	require.NoError(t, os.Chtimes(markerPath, fixedWitnessNow, fixedWitnessNow))

	home := &Home{Dir: dir, SQLiteDir: dir}
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedWitnessNow }, func(pid int) bool { return pid == 4242 }, DefaultTranscodeCaps(),
	)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.NotEmpty(t, active)
}

func TestActiveWritersBlocksOnMalformedFreshCompressionMarker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dotcodex")
	tmpDir := filepath.Join(dir, tmpSubdir)
	require.NoError(t, os.MkdirAll(tmpDir, 0o750))
	markerPath := filepath.Join(tmpDir, compressionLockFileName)
	require.NoError(t, os.WriteFile(markerPath, []byte("not-a-marker"), 0o600))
	require.NoError(t, os.Chtimes(markerPath, fixedWitnessNow, fixedWitnessNow))
	home := &Home{Dir: dir, SQLiteDir: dir}
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedWitnessNow }, func(int) bool { return false }, DefaultTranscodeCaps(),
	)

	_, err := workspace.ActiveWriters()

	assert.ErrorIs(t, err, tool.ErrNoWitness)
}

func TestActiveWritersIgnoresStaleCompressionMarker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dotcodex")
	tmpDir := filepath.Join(dir, tmpSubdir)
	require.NoError(t, os.MkdirAll(tmpDir, 0o750))
	markerPath := filepath.Join(tmpDir, compressionLockFileName)
	require.NoError(t, os.WriteFile(markerPath, []byte("pid=4242 started_at=fixture\n"), 0o600))
	stale := fixedWitnessNow.Add(-7 * time.Hour)
	require.NoError(t, os.Chtimes(markerPath, stale, stale))

	home := &Home{Dir: dir, SQLiteDir: dir}
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedWitnessNow }, func(pid int) bool { return pid == 4242 }, DefaultTranscodeCaps(),
	)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.Empty(t, active, "a marker outside the six-hour window is never evidence, even with a live pid")
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
	workspace := newWorkspace(
		home, fakeGetenv(nil), noProcesses, func() time.Time { return fixedWitnessNow }, func(int) bool { return false }, DefaultTranscodeCaps(),
	)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.NotEmpty(t, active)
}
