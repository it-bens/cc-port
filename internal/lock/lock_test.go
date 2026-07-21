package lock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

func newTestLockPath(t *testing.T) string {
	t.Helper()
	toolDir := filepath.Join(t.TempDir(), "tool-state")
	require.NoError(t, os.MkdirAll(toolDir, 0o750))
	return filepath.Join(toolDir, FileName)
}

func noActive() ([]tool.ActiveWriter, error) {
	return nil, nil
}

func TestWithLock_SucceedsWithNoSessions(t *testing.T) {
	lockPath := newTestLockPath(t)

	err := WithLock(lockPath, noActive, func() error { return nil })
	require.NoError(t, err)
}

func TestWithLock_PersistsLockFileOnSuccess(t *testing.T) {
	lockPath := newTestLockPath(t)

	err := WithLock(lockPath, noActive, func() error { return nil })
	require.NoError(t, err)

	assert.FileExists(t, lockPath)
}

func TestHeld_SecondReleaseIsNoOpAndLockFilePersists(t *testing.T) {
	lockPath := newTestLockPath(t)
	held, err := Acquire(lockPath, noActive)
	require.NoError(t, err)

	require.NoError(t, held.Release())
	require.NoError(t, held.Release())
	assert.FileExists(t, lockPath)
}

func TestWithLock_PersistsLockFileOnFnError(t *testing.T) {
	lockPath := newTestLockPath(t)

	boom := errors.New("boom")
	err := WithLock(lockPath, noActive, func() error { return boom })
	require.ErrorIs(t, err, boom)

	assert.FileExists(t, lockPath)
}

func TestWithLock_AbortsWhenSessionPIDIsAlive(t *testing.T) {
	lockPath := newTestLockPath(t)

	err := WithLock(lockPath, func() ([]tool.ActiveWriter, error) {
		return []tool.ActiveWriter{{Pid: os.Getpid(), Cwd: "/test/project"}}, nil
	}, func() error { return nil })

	var liveErr *LiveSessionsError
	require.ErrorAs(t, err, &liveErr)
	assert.Len(t, liveErr.Sessions, 1)
	assert.ErrorContains(t, err, "live writer process")
}

func TestWithLock_AbortsWhenAnotherCCPortHoldsTheLock(t *testing.T) {
	lockPath := newTestLockPath(t)

	// Hold the lock from a sibling flock.Flock. In a real scenario this
	// is a second cc-port process; in-test we reuse the same path. Linux
	// and Darwin both use syscall.Flock under the hood, which is per-fd
	// (not per-process like fcntl F_SETLK), so two in-process flock.Flock
	// instances on the same path contend as expected.
	sibling := flock.New(lockPath)
	ok, err := sibling.TryLock()
	require.NoError(t, err)
	require.True(t, ok)
	defer func() { _ = sibling.Unlock() }()

	err = WithLock(lockPath, noActive, func() error { return nil })
	require.ErrorIs(t, err, ErrConcurrentInvocation)
	assert.ErrorContains(t, err, "this tool's state")
}

func TestAcquire_SecondCallObservesFirstHold(t *testing.T) {
	lockPath := newTestLockPath(t)
	first, err := Acquire(lockPath, noActive)
	require.NoError(t, err)
	defer func() { _ = first.Release() }()

	second, err := Acquire(lockPath, noActive)

	assert.Nil(t, second)
	require.ErrorIs(t, err, ErrConcurrentInvocation)
}

func TestWithLock_SucceedsAfterPreviousReleased(t *testing.T) {
	// The first call leaves the lock file in place, so the second reuses its
	// inode and competes on the same flock.
	lockPath := newTestLockPath(t)

	require.NoError(t, WithLock(lockPath, noActive, func() error { return nil }))
	assert.FileExists(t, lockPath)
	require.NoError(t, WithLock(lockPath, noActive, func() error { return nil }))
}

func TestWithLock_CallsFn(t *testing.T) {
	lockPath := newTestLockPath(t)

	var fnCalled bool
	err := WithLock(lockPath, noActive, func() error {
		fnCalled = true
		// While the outer lock is held, a sibling flock.Flock on the same
		// path must fail to acquire (per-fd flock semantics on Linux/Darwin).
		sibling := flock.New(lockPath)
		ok, lockErr := sibling.TryLock()
		require.NoError(t, lockErr)
		assert.False(t, ok, "sibling flock must report not-locked while WithLock holds the lock")
		return nil
	})
	require.NoError(t, err)
	assert.True(t, fnCalled, "fn must be invoked on the success path")
}

func TestWithLock_ReleasesOnFnError(t *testing.T) {
	lockPath := newTestLockPath(t)

	boom := errors.New("boom")
	err := WithLock(lockPath, noActive, func() error { return boom })
	require.ErrorIs(t, err, boom)

	// A subsequent WithLock must succeed — release ran despite fn's error.
	require.NoError(t, WithLock(lockPath, noActive, func() error { return nil }))
}

func TestWithLock_PropagatesAcquireError(t *testing.T) {
	lockPath := newTestLockPath(t)

	var fnCalled bool
	err := WithLock(lockPath, func() ([]tool.ActiveWriter, error) {
		return []tool.ActiveWriter{{Pid: os.Getpid(), Cwd: "/test/project"}}, nil
	}, func() error {
		fnCalled = true
		return nil
	})
	var liveErr *LiveSessionsError
	require.ErrorAs(t, err, &liveErr)
	assert.False(t, fnCalled, "fn must not be invoked when acquire fails")
}

func TestWithLock_ReleasesAfterRecoveredPanic(t *testing.T) {
	lockPath := newTestLockPath(t)

	func() {
		defer func() {
			_ = recover() // swallow the synthetic panic so the test can proceed
		}()

		_ = WithLock(lockPath, noActive, func() error {
			panic("synthetic panic inside fn")
		})
	}()

	// If the defer-based Unlock worked, the lock is now free and a
	// second WithLock call must succeed.
	err := WithLock(lockPath, noActive, func() error { return nil })
	require.NoError(t, err, "second WithLock must succeed — the first call's lock should be released")
}

func TestWithLock_ReleaseErrorSurfacesOnFnSuccess(t *testing.T) {
	lockPath := newTestLockPath(t)

	originalUnlock := unlockFn
	t.Cleanup(func() { unlockFn = originalUnlock })
	injectedUnlockErr := errors.New("synthetic unlock failure")
	unlockFn = func(*flock.Flock) error {
		return injectedUnlockErr
	}

	err := WithLock(lockPath, noActive, func() error { return nil })
	require.ErrorIs(t, err, ErrUnlockFailure)
	require.ErrorIs(t, err, injectedUnlockErr)
}

func TestWithLock_ReleaseErrorSuppressedOnFnError(t *testing.T) {
	lockPath := newTestLockPath(t)

	originalUnlock := unlockFn
	t.Cleanup(func() { unlockFn = originalUnlock })
	unlockFn = func(*flock.Flock) error {
		return errors.New("synthetic unlock failure")
	}

	fnErr := errors.New("fn returned this")
	err := WithLock(lockPath, noActive, func() error { return fnErr })
	require.ErrorIs(t, err, fnErr)
	assert.NotErrorIs(t, err, ErrUnlockFailure)
}
