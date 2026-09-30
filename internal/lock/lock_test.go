package lock

import (
	"errors"
	"fmt"
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
	assert.ErrorContains(t, err, "live writer(s)")
}

// recheckWorkspace stands in for a tool workspace. The recheck reaches only
// ActiveWriters, so the embedded interface stays nil.
type recheckWorkspace struct {
	tool.Workspace
	writers []tool.ActiveWriter
	err     error
}

func (workspace *recheckWorkspace) ActiveWriters() ([]tool.ActiveWriter, error) {
	return workspace.writers, workspace.err
}

func TestRecheckActiveWriters_AllQuietReturnsNil(t *testing.T) {
	targets := []tool.Target{
		{Workspace: &recheckWorkspace{}},
		{Workspace: &recheckWorkspace{}},
	}

	require.NoError(t, RecheckActiveWriters(targets, nil))
}

func TestRecheckActiveWriters_AggregatesLiveWritersInTargetOrder(t *testing.T) {
	targets := []tool.Target{
		{Workspace: &recheckWorkspace{writers: []tool.ActiveWriter{{Pid: 101, Cwd: "/writer/alpha"}}}},
		{Workspace: &recheckWorkspace{}},
		{Workspace: &recheckWorkspace{writers: []tool.ActiveWriter{{Pid: 202, Cwd: "/writer/beta"}}}},
	}

	err := RecheckActiveWriters(targets, nil)

	var liveErr *LiveSessionsError
	require.ErrorAs(t, err, &liveErr)
	assert.Equal(t,
		[]tool.ActiveWriter{{Pid: 101, Cwd: "/writer/alpha"}, {Pid: 202, Cwd: "/writer/beta"}},
		liveErr.Sessions,
		"live writers from every target must aggregate into one error in target order")
}

func TestRecheckActiveWriters_ScanFailureDoesNotHideLiveWriters(t *testing.T) {
	scanFailure := errors.New("witness backend gone")
	targets := []tool.Target{
		{Workspace: &recheckWorkspace{err: scanFailure}},
		{Workspace: &recheckWorkspace{writers: []tool.ActiveWriter{{Pid: 303, Cwd: "/writer/gamma"}}}},
	}

	err := RecheckActiveWriters(targets, nil)

	require.ErrorIs(t, err, scanFailure)
	var liveErr *LiveSessionsError
	require.ErrorAs(t, err, &liveErr)
	assert.Equal(t, []tool.ActiveWriter{{Pid: 303, Cwd: "/writer/gamma"}}, liveErr.Sessions)
}

// recheckTool stands in for a tool. The collector reaches only Name, so the
// embedded interface stays nil.
type recheckTool struct {
	tool.Tool
	name string
}

func (fake *recheckTool) Name() string { return fake.name }

func TestRecheckActiveWriters_CollectorRecordsLiveWritersAndReturnsNil(t *testing.T) {
	targets := []tool.Target{
		{Tool: &recheckTool{name: "claude"}, Workspace: &recheckWorkspace{writers: []tool.ActiveWriter{{Pid: 101, Cwd: "/writer/alpha"}}}},
		{Tool: &recheckTool{name: "codex"}, Workspace: &recheckWorkspace{writers: []tool.ActiveWriter{{Detail: "busy database state_5.sqlite"}}}},
	}
	ignored := &IgnoredWriters{}

	err := RecheckActiveWriters(targets, ignored)

	require.NoError(t, err)
	assert.Equal(t, []IgnoredWriter{
		{Tool: "claude", Writer: tool.ActiveWriter{Pid: 101, Cwd: "/writer/alpha"}},
		{Tool: "codex", Writer: tool.ActiveWriter{Detail: "busy database state_5.sqlite"}},
	}, ignored.List())
}

func TestRecheckActiveWriters_CollectorStillReturnsScanFailure(t *testing.T) {
	scanFailure := fmt.Errorf("%w: sessions directory unreadable", tool.ErrNoWitness)
	targets := []tool.Target{
		{Tool: &recheckTool{name: "claude"}, Workspace: &recheckWorkspace{err: scanFailure}},
		{Tool: &recheckTool{name: "codex"}, Workspace: &recheckWorkspace{writers: []tool.ActiveWriter{{Pid: 303}}}},
	}
	ignored := &IgnoredWriters{}

	err := RecheckActiveWriters(targets, ignored)

	require.ErrorIs(t, err, scanFailure)
	var liveErr *LiveSessionsError
	assert.NotErrorAs(t, err, &liveErr, "an ignored live writer must not surface as a LiveSessionsError")
	assert.Equal(t, []IgnoredWriter{{Tool: "codex", Writer: tool.ActiveWriter{Pid: 303}}}, ignored.List())
}

func TestWitnessFor_NilCollectorPassesWritersThrough(t *testing.T) {
	writers := []tool.ActiveWriter{{Pid: 42, Cwd: "/work/other"}, {Detail: "busy database state_5.sqlite"}}
	target := tool.Target{Tool: &recheckTool{name: "codex"}, Workspace: &recheckWorkspace{writers: writers}}

	active, err := WitnessFor(target, nil)()

	require.NoError(t, err)
	assert.Equal(t, writers, active)
}

func TestWitnessFor_CollectorRecordsWritersAndReportsNone(t *testing.T) {
	writers := []tool.ActiveWriter{{Pid: 42, Cwd: "/work/other"}, {Detail: "busy database state_5.sqlite"}}
	target := tool.Target{Tool: &recheckTool{name: "codex"}, Workspace: &recheckWorkspace{writers: writers}}
	ignored := &IgnoredWriters{}

	active, err := WitnessFor(target, ignored)()

	require.NoError(t, err)
	assert.Empty(t, active)
	assert.Equal(t, []IgnoredWriter{
		{Tool: "codex", Writer: tool.ActiveWriter{Pid: 42, Cwd: "/work/other"}},
		{Tool: "codex", Writer: tool.ActiveWriter{Detail: "busy database state_5.sqlite"}},
	}, ignored.List())
}

func TestWitnessFor_NilCollectorPassesWitnessErrorThroughUnchanged(t *testing.T) {
	witnessErr := fmt.Errorf("%w: sessions directory unreadable", tool.ErrNoWitness)
	target := tool.Target{Tool: &recheckTool{name: "claude"}, Workspace: &recheckWorkspace{err: witnessErr}}

	_, err := WitnessFor(target, nil)()

	require.ErrorIs(t, err, tool.ErrNoWitness)
	assert.Same(t, witnessErr, err)
}

func TestWitnessFor_CollectorPassesWitnessErrorThroughAndRecordsNothing(t *testing.T) {
	witnessErr := fmt.Errorf("%w: sessions directory unreadable", tool.ErrNoWitness)
	target := tool.Target{
		Tool:      &recheckTool{name: "claude"},
		Workspace: &recheckWorkspace{writers: []tool.ActiveWriter{{Pid: 42}}, err: witnessErr},
	}
	ignored := &IgnoredWriters{}

	_, err := WitnessFor(target, ignored)()

	require.ErrorIs(t, err, tool.ErrNoWitness)
	assert.Same(t, witnessErr, err)
	assert.Empty(t, ignored.List())
}

// The preflight witness and the re-check report the same writers, so each
// target is witnessed twice here, as a move or import does.
func TestIgnoredWritersList_DeduplicatesInFirstRecordedOrder(t *testing.T) {
	claudeTarget := tool.Target{
		Tool:      &recheckTool{name: "claude"},
		Workspace: &recheckWorkspace{writers: []tool.ActiveWriter{{Pid: 42, Cwd: "/work/other"}}},
	}
	codexTarget := tool.Target{
		Tool: &recheckTool{name: "codex"},
		Workspace: &recheckWorkspace{writers: []tool.ActiveWriter{
			{Detail: "busy database state_5.sqlite"},
			{Detail: "busy database logs_2.sqlite"},
		}},
	}
	ignored := &IgnoredWriters{}

	for range 2 {
		for _, target := range []tool.Target{claudeTarget, codexTarget} {
			_, err := WitnessFor(target, ignored)()
			require.NoError(t, err)
		}
	}

	assert.Equal(t, []IgnoredWriter{
		{Tool: "claude", Writer: tool.ActiveWriter{Pid: 42, Cwd: "/work/other"}},
		{Tool: "codex", Writer: tool.ActiveWriter{Detail: "busy database state_5.sqlite"}},
		{Tool: "codex", Writer: tool.ActiveWriter{Detail: "busy database logs_2.sqlite"}},
	}, ignored.List())
}

// The rendered line names the tool, so Tool is part of the dedup key: one
// writer descriptor reported by two tools is two entries, not one.
func TestIgnoredWritersList_KeepsTheSameWriterOncePerTool(t *testing.T) {
	writer := tool.ActiveWriter{Pid: 42, Cwd: "/work/other"}
	ignored := &IgnoredWriters{}

	for _, name := range []string{"claude", "codex"} {
		target := tool.Target{
			Tool:      &recheckTool{name: name},
			Workspace: &recheckWorkspace{writers: []tool.ActiveWriter{writer}},
		}
		_, err := WitnessFor(target, ignored)()
		require.NoError(t, err)
	}

	assert.Equal(t, []IgnoredWriter{
		{Tool: "claude", Writer: writer},
		{Tool: "codex", Writer: writer},
	}, ignored.List())
}

func TestLiveSessionsError_ListsEveryWriter(t *testing.T) {
	liveErr := &LiveSessionsError{Sessions: []tool.ActiveWriter{
		{Pid: 42, Cwd: "/work/other"},
		{Detail: "busy database state_5.sqlite"},
	}}

	assert.Equal(t,
		`refusing to run: 2 live writer(s) detected: [pid=42 cwd="/work/other"; busy database state_5.sqlite]`,
		liveErr.Error())
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
