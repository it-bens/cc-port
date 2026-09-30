//go:build darwin || linux

// Package lock guards tool state against concurrent cc-port runs and live
// writers.
package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"

	"github.com/it-bens/cc-port/internal/tool"
)

// FileName is the name of the advisory-lock file cc-port creates inside a
// tool's state directory.
const FileName = ".cc-port.lock"

// unlockFn is the function used to release the advisory lock. Tests swap
// it to simulate a release-time failure; production always uses
// (*flock.Flock).Unlock.
var unlockFn = (*flock.Flock).Unlock

// ErrConcurrentInvocation is returned by WithLock when another cc-port run
// already holds a tool's advisory lock. Callers discriminate via errors.Is;
// the wrapping message names the contended lock directory.
var ErrConcurrentInvocation = errors.New("another cc-port invocation is operating on this tool's state")

// ErrUnlockFailure is returned by WithLock when releasing the advisory lock
// fails on the fn-success path. The wrapping error joins the underlying
// unlock cause via %w, so errors.Is matches both this sentinel and the cause.
var ErrUnlockFailure = errors.New("release cc-port lock")

// LiveSessionsError reports one or more detected live writers. Acquire and
// WithLock return it before taking the lock; RecheckActiveWriters returns it,
// with writers aggregated across all targets, while the locks are held.
// Sessions carries the witness list; callers inspect it via errors.As.
type LiveSessionsError struct {
	Sessions []tool.ActiveWriter
}

// Witness gathers a tool's liveness evidence. A non-empty result refuses
// Acquire and WithLock; an error refuses them too.
type Witness func() ([]tool.ActiveWriter, error)

// IgnoredWriter is a live writer a witness reported and the caller chose to
// proceed past. Tool is the tool's wire name.
type IgnoredWriter struct {
	Tool   string
	Writer tool.ActiveWriter
}

// IgnoredWriters collects the writers ignored across every witness run of
// one command invocation. A nil *IgnoredWriters is the refusing mode.
type IgnoredWriters struct {
	writers []IgnoredWriter
}

// List returns the ignored writers in first-recorded order, each
// (Tool, Pid, Cwd, Detail) tuple once: the preflight witness and the
// re-check usually report the same writers.
func (ignored *IgnoredWriters) List() []IgnoredWriter {
	seen := make(map[IgnoredWriter]bool, len(ignored.writers))
	var unique []IgnoredWriter
	for _, writer := range ignored.writers {
		if seen[writer] {
			continue
		}
		seen[writer] = true
		unique = append(unique, writer)
	}
	return unique
}

// WitnessFor returns the witness for target. With a nil ignored it is
// target.Workspace.ActiveWriters. Otherwise the witness records every writer
// the workspace reports under target.Tool.Name() and reports none; a
// witness error passes through unchanged, so a witness that cannot run
// still refuses.
func WitnessFor(target tool.Target, ignored *IgnoredWriters) Witness {
	if ignored == nil {
		return target.Workspace.ActiveWriters
	}
	return func() ([]tool.ActiveWriter, error) {
		active, err := target.Workspace.ActiveWriters()
		if err != nil {
			return nil, err
		}
		for _, writer := range active {
			ignored.writers = append(ignored.writers, IgnoredWriter{Tool: target.Tool.Name(), Writer: writer})
		}
		return nil, nil
	}
}

// Held is an acquired cc-port advisory lock. Release frees it after the
// protected work completes; later calls are no-ops.
type Held struct {
	fileLock *flock.Flock
	released bool
}

func (e *LiveSessionsError) Error() string {
	descriptors := make([]string, len(e.Sessions))
	for index, session := range e.Sessions {
		descriptors[index] = session.String()
	}
	return fmt.Sprintf(
		"refusing to run: %d live writer(s) detected: [%s]",
		len(e.Sessions),
		strings.Join(descriptors, "; "),
	)
}

// Acquire runs witness before acquiring the advisory lock and retains the
// flock until the caller releases it.
func Acquire(lockPath string, witness Witness) (*Held, error) {
	if witness == nil {
		return nil, fmt.Errorf("witness is required")
	}
	active, err := witness()
	if err != nil {
		return nil, fmt.Errorf("scan active writers: %w", err)
	}
	if len(active) > 0 {
		return nil, &LiveSessionsError{Sessions: active}
	}

	lockDir := filepath.Dir(lockPath)
	if err := os.MkdirAll(lockDir, 0o750); err != nil {
		return nil, fmt.Errorf("ensure lock directory exists: %w", err)
	}

	fileLock := flock.New(lockPath)

	ok, err := fileLock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire cc-port lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrConcurrentInvocation, lockDir)
	}
	return &Held{fileLock: fileLock}, nil
}

// RecheckActiveWriters re-runs every target's WitnessFor(target, ignored)
// and aggregates the results: every scan failure and, when any target
// reports live writers, one LiveSessionsError carrying all of them join into
// the returned error. With a non-nil ignored, live writers are recorded
// instead and never produce a LiveSessionsError. A caller holding several
// tools' flocks inserts it immediately before a batch write, because the
// lock-time witness evidence goes stale while the caller works and the flock
// does not stop the tools themselves from starting.
func RecheckActiveWriters(targets []tool.Target, ignored *IgnoredWriters) error {
	var sessions []tool.ActiveWriter
	var errs []error
	for _, target := range targets {
		active, err := WitnessFor(target, ignored)()
		if err != nil {
			errs = append(errs, fmt.Errorf("scan active writers: %w", err))
			continue
		}
		sessions = append(sessions, active...)
	}
	if len(sessions) > 0 {
		errs = append(errs, &LiveSessionsError{Sessions: sessions})
	}
	return errors.Join(errs...)
}

// Release unlocks the advisory lock.
func (held *Held) Release() error {
	if held == nil || held.fileLock == nil {
		return fmt.Errorf("release nil cc-port lock")
	}
	if held.released {
		return nil
	}
	held.released = true
	if err := unlockFn(held.fileLock); err != nil {
		return fmt.Errorf("%w: %w", ErrUnlockFailure, err)
	}
	return nil
}

// WithLock runs witness before acquiring the advisory lock.
func WithLock(
	lockPath string,
	witness Witness,
	fn func() error,
) (returnErr error) {
	held, err := Acquire(lockPath, witness)
	if err != nil {
		return err
	}

	defer func() {
		releaseErr := held.Release()
		if returnErr != nil {
			return
		}
		returnErr = releaseErr
	}()

	return fn()
}
