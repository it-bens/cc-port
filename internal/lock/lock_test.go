package lock

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
)

func newTestHome(t *testing.T) *claude.Home {
	t.Helper()
	claudeDir := filepath.Join(t.TempDir(), "dotclaude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o750))
	return &claude.Home{
		Dir:        claudeDir,
		ConfigFile: claudeDir + ".json",
	}
}

func writeSessionFile(t *testing.T, claudeHome *claude.Home, name string, pid int) {
	t.Helper()

	sessionsDir := claudeHome.SessionsDir()
	require.NoError(t, os.MkdirAll(sessionsDir, 0o750))

	sessionFile := claude.SessionFile{Cwd: "/test/project", Pid: pid}
	data, err := json.Marshal(sessionFile)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(sessionsDir, name+".json"), data, 0600))
}

func TestWithLock_SucceedsWithNoSessions(t *testing.T) {
	claudeHome := newTestHome(t)

	err := WithLock(claudeHome, func() error { return nil })
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(claudeHome.Dir, FileName))
}

func TestWithLock_SucceedsWhenSessionPIDIsDead(t *testing.T) {
	claudeHome := newTestHome(t)
	// A PID above every modern OS's pid_max is guaranteed dead.
	writeSessionFile(t, claudeHome, "stale", 2_000_000_001)

	err := WithLock(claudeHome, func() error { return nil })
	require.NoError(t, err)
}

func TestWithLock_AbortsWhenSessionPIDIsAlive(t *testing.T) {
	claudeHome := newTestHome(t)
	// os.Getpid() is this test process — guaranteed alive.
	writeSessionFile(t, claudeHome, "live", os.Getpid())

	err := WithLock(claudeHome, func() error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "live Claude Code session")
}

func TestWithLock_AbortsWhenAnotherCCPortHoldsTheLock(t *testing.T) {
	claudeHome := newTestHome(t)

	// Hold the lock from a sibling flock.Flock. In a real scenario this
	// is a second cc-port process; in-test we reuse the same path. Linux
	// and Darwin both use syscall.Flock under the hood, which is per-fd
	// (not per-process like fcntl F_SETLK), so two in-process flock.Flock
	// instances on the same path contend as expected.
	require.NoError(t, os.MkdirAll(claudeHome.Dir, 0o750))
	sibling := flock.New(filepath.Join(claudeHome.Dir, FileName))
	ok, err := sibling.TryLock()
	require.NoError(t, err)
	require.True(t, ok)
	defer func() { _ = sibling.Unlock() }()

	err = WithLock(claudeHome, func() error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another cc-port invocation")
}

func TestWithLock_SucceedsAfterPreviousReleased(t *testing.T) {
	claudeHome := newTestHome(t)

	require.NoError(t, WithLock(claudeHome, func() error { return nil }))
	require.NoError(t, WithLock(claudeHome, func() error { return nil }))
}

func TestWithLock_CallsFn(t *testing.T) {
	claudeHome := newTestHome(t)

	var fnCalled bool
	err := WithLock(claudeHome, func() error {
		fnCalled = true
		// While the outer lock is held, a sibling flock.Flock on the same
		// path must fail to acquire (per-fd flock semantics on Linux/Darwin).
		sibling := flock.New(filepath.Join(claudeHome.Dir, FileName))
		ok, lockErr := sibling.TryLock()
		require.NoError(t, lockErr)
		assert.False(t, ok, "sibling flock must report not-locked while WithLock holds the lock")
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

func TestWithLock_ReleasesAfterRecoveredPanic(t *testing.T) {
	claudeHome := newTestHome(t)

	func() {
		defer func() {
			_ = recover() // swallow the synthetic panic so the test can proceed
		}()

		_ = WithLock(claudeHome, func() error {
			panic("synthetic panic inside fn")
		})
	}()

	// If the defer-based Unlock worked, the lock is now free and a
	// second WithLock call must succeed.
	err := WithLock(claudeHome, func() error { return nil })
	require.NoError(t, err, "second WithLock must succeed — the first call's lock should be released")
}

func TestWithLock_ReleaseErrorSurfacesOnFnSuccess(t *testing.T) {
	claudeHome := newTestHome(t)

	originalUnlock := unlockFn
	t.Cleanup(func() { unlockFn = originalUnlock })
	unlockFn = func(*flock.Flock) error {
		return errors.New("synthetic unlock failure")
	}

	err := WithLock(claudeHome, func() error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "release cc-port lock")
	assert.Contains(t, err.Error(), "synthetic unlock failure")
}

func TestWithLock_ReleaseErrorSuppressedOnFnError(t *testing.T) {
	claudeHome := newTestHome(t)

	originalUnlock := unlockFn
	t.Cleanup(func() { unlockFn = originalUnlock })
	unlockFn = func(*flock.Flock) error {
		return errors.New("synthetic unlock failure")
	}

	fnErr := errors.New("fn returned this")
	err := WithLock(claudeHome, func() error { return fnErr })
	require.Error(t, err)
	require.ErrorIs(t, err, fnErr)
	assert.NotContains(t, err.Error(), "release cc-port lock")
}
