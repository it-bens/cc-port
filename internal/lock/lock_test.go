package lock

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
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

	lockHandle, err := acquire(claudeHome)
	require.NoError(t, err)
	defer func() { _ = lockHandle.release() }()

	assert.FileExists(t, filepath.Join(claudeHome.Dir, LockFileName))
}

func TestAcquire_SucceedsWhenSessionPIDIsDead(t *testing.T) {
	claudeHome := newTestHome(t)
	// A PID above every modern OS's pid_max is guaranteed dead.
	writeSessionFile(t, claudeHome, "stale", 2_000_000_001)

	lockHandle, err := acquire(claudeHome)
	require.NoError(t, err)
	_ = lockHandle.release()
}

func TestAcquire_AbortsWhenSessionPIDIsAlive(t *testing.T) {
	claudeHome := newTestHome(t)
	// os.Getpid() is this test process — guaranteed alive.
	writeSessionFile(t, claudeHome, "live", os.Getpid())

	_, err := acquire(claudeHome)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "live Claude Code session")
}

func TestAcquire_AbortsWhenAnotherCCPortHoldsTheLock(t *testing.T) {
	claudeHome := newTestHome(t)

	firstLock, err := acquire(claudeHome)
	require.NoError(t, err)
	defer func() { _ = firstLock.release() }()

	_, err = acquire(claudeHome)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another cc-port invocation")
}

func TestAcquire_SucceedsAfterPreviousReleased(t *testing.T) {
	claudeHome := newTestHome(t)

	firstLock, err := acquire(claudeHome)
	require.NoError(t, err)
	require.NoError(t, firstLock.release())

	secondLock, err := acquire(claudeHome)
	require.NoError(t, err)
	_ = secondLock.release()
}

func TestLock_ReleaseIsIdempotent(t *testing.T) {
	claudeHome := newTestHome(t)

	lockHandle, err := acquire(claudeHome)
	require.NoError(t, err)

	require.NoError(t, lockHandle.release())
	require.NoError(t, lockHandle.release())
}

func TestWithLock_CallsFn(t *testing.T) {
	claudeHome := newTestHome(t)

	var fnCalled bool
	err := WithLock(claudeHome, func() error {
		fnCalled = true
		// While the outer lock is held, a nested WithLock on the same home
		// must hit EWOULDBLOCK inside acquire and surface the "another
		// cc-port invocation" abort message.
		nestedErr := WithLock(claudeHome, func() error {
			t.Fatal("nested fn must not run while the outer lock is held")
			return nil
		})
		require.Error(t, nestedErr)
		assert.Contains(t, nestedErr.Error(), "another cc-port invocation")
		return nil
	})
	require.NoError(t, err)
	assert.True(t, fnCalled, "fn must be invoked on the success path")
}

func TestWithLock_ReleasesOnFnSuccess(t *testing.T) {
	claudeHome := newTestHome(t)

	require.NoError(t, WithLock(claudeHome, func() error { return nil }))
	require.NoError(t, WithLock(claudeHome, func() error { return nil }))
}

func TestWithLock_ReleasesOnFnError(t *testing.T) {
	claudeHome := newTestHome(t)

	boom := errors.New("boom")
	err := WithLock(claudeHome, func() error { return boom })
	require.ErrorIs(t, err, boom)

	// A subsequent WithLock must succeed — release ran despite fn's error.
	require.NoError(t, WithLock(claudeHome, func() error { return nil }))
}

func TestWithLock_PropagatesAcquireError(t *testing.T) {
	claudeHome := newTestHome(t)
	writeSessionFile(t, claudeHome, "live", os.Getpid())

	var fnCalled bool
	err := WithLock(claudeHome, func() error {
		fnCalled = true
		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "live Claude Code session")
	assert.False(t, fnCalled, "fn must not be invoked when acquire fails")
}

func TestWithLock_ReturnsFnErrorOverReleaseError(t *testing.T) {
	claudeHome := newTestHome(t)

	// Swap the close hook so release still releases the flock but reports a
	// synthetic error. This is the test-injected release-failure seam the
	// design spec authorizes — see internal/lock/README.md §Contracts.
	originalCloser := closeLockFile
	closeLockFile = func(file *os.File) error {
		_ = originalCloser(file) // actually release the kernel flock
		return errors.New("simulated release failure")
	}
	defer func() { closeLockFile = originalCloser }()

	fnErr := errors.New("fn failed")
	err := WithLock(claudeHome, func() error { return fnErr })
	require.ErrorIs(t, err, fnErr, "fn error must win over release error on the fn-error path")
}
