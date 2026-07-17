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

// LiveSessionsError is returned by WithLock when one or more live writers are
// detected before the lock is taken. Sessions carries the witness
// list; callers inspect it via errors.As. WithLock takes the lock only when the
// list is empty.
type LiveSessionsError struct {
	Sessions []tool.ActiveWriter
}

// Held is an acquired cc-port advisory lock. Release frees it after the
// protected work completes; later calls are no-ops.
type Held struct {
	fileLock *flock.Flock
	path     string
	released bool
}

func (e *LiveSessionsError) Error() string {
	descriptors := make([]string, len(e.Sessions))
	for index, session := range e.Sessions {
		descriptors[index] = fmt.Sprintf("pid=%d cwd=%q", session.Pid, session.Cwd)
	}
	return fmt.Sprintf(
		"refusing to run: %d live writer process(es) detected: [%s]",
		len(e.Sessions),
		strings.Join(descriptors, "; "),
	)
}

// Acquire runs witness before acquiring the advisory lock and retains the
// flock until the caller releases it.
func Acquire(lockPath string, witness func() ([]tool.ActiveWriter, error)) (*Held, error) {
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
	return &Held{fileLock: fileLock, path: lockPath}, nil
}

// Release unlocks the advisory lock and removes its lock file.
func (held *Held) Release() error {
	if held == nil || held.fileLock == nil {
		return fmt.Errorf("release nil cc-port lock")
	}
	if held.released {
		return nil
	}
	held.released = true
	unlockErr := unlockFn(held.fileLock)
	removeErr := os.Remove(held.path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	if unlockErr != nil {
		return errors.Join(fmt.Errorf("%w: %w", ErrUnlockFailure, unlockErr), removeErr)
	}
	if removeErr != nil {
		return fmt.Errorf("remove cc-port lock file: %w", removeErr)
	}
	return nil
}

// WithLock runs witness before acquiring the advisory lock.
func WithLock(
	lockPath string,
	witness func() ([]tool.ActiveWriter, error),
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
