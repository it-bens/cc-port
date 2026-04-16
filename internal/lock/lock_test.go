package lock_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/lock"
)

// newTestHome sets up a Home rooted at t.TempDir() so each test has an
// isolated ~/.claude to operate on.
func newTestHome(t *testing.T) *claude.Home {
	t.Helper()
	claudeDir := filepath.Join(t.TempDir(), "dotclaude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o750))
	return &claude.Home{
		Dir:        claudeDir,
		ConfigFile: claudeDir + ".json",
	}
}

// writeSessionFile writes a sessions/<name>.json file with the given PID so
// tests can simulate a Claude Code session entry.
func writeSessionFile(t *testing.T, claudeHome *claude.Home, name string, pid int) {
	t.Helper()

	sessionsDir := claudeHome.SessionsDir()
	require.NoError(t, os.MkdirAll(sessionsDir, 0o750))

	sessionFile := claude.SessionFile{Cwd: "/test/project", Pid: pid}
	data, err := json.Marshal(sessionFile)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(sessionsDir, name+".json"), data, 0600))
}

func TestAcquire_SucceedsWithNoSessions(t *testing.T) {
	claudeHome := newTestHome(t)

	lockHandle, err := lock.Acquire(claudeHome)
	require.NoError(t, err)
	defer func() { _ = lockHandle.Release() }()

	assert.FileExists(t, filepath.Join(claudeHome.Dir, lock.LockFileName))
}

func TestAcquire_SucceedsWhenSessionPIDIsDead(t *testing.T) {
	claudeHome := newTestHome(t)
	// A PID above every modern OS's pid_max is guaranteed dead.
	writeSessionFile(t, claudeHome, "stale", 2_000_000_001)

	lockHandle, err := lock.Acquire(claudeHome)
	require.NoError(t, err)
	_ = lockHandle.Release()
}

func TestAcquire_AbortsWhenSessionPIDIsAlive(t *testing.T) {
	claudeHome := newTestHome(t)
	// os.Getpid() is this test process — guaranteed alive.
	writeSessionFile(t, claudeHome, "live", os.Getpid())

	_, err := lock.Acquire(claudeHome)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "live Claude Code session")
}

func TestAcquire_AbortsWhenAnotherCCPortHoldsTheLock(t *testing.T) {
	claudeHome := newTestHome(t)

	firstLock, err := lock.Acquire(claudeHome)
	require.NoError(t, err)
	defer func() { _ = firstLock.Release() }()

	_, err = lock.Acquire(claudeHome)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another cc-port invocation")
}

func TestAcquire_SucceedsAfterPreviousReleased(t *testing.T) {
	claudeHome := newTestHome(t)

	firstLock, err := lock.Acquire(claudeHome)
	require.NoError(t, err)
	require.NoError(t, firstLock.Release())

	secondLock, err := lock.Acquire(claudeHome)
	require.NoError(t, err)
	_ = secondLock.Release()
}

func TestLock_ReleaseIsIdempotent(t *testing.T) {
	claudeHome := newTestHome(t)

	lockHandle, err := lock.Acquire(claudeHome)
	require.NoError(t, err)

	require.NoError(t, lockHandle.Release())
	require.NoError(t, lockHandle.Release())
}
