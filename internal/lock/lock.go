//go:build darwin || linux

// Package lock guards ~/.claude against concurrent cc-port runs and live
// Claude Code sessions.
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

// FileName is the name of the advisory-lock file cc-port creates inside
// the Claude Code home directory.
const FileName = ".cc-port.lock"

// unlockFn is the function used to release the advisory lock. Tests swap
// it to simulate a release-time failure; production always uses
// (*flock.Flock).Unlock.
var unlockFn = (*flock.Flock).Unlock

// ErrConcurrentInvocation is returned by WithLock when another cc-port run
// already holds the advisory lock. Callers discriminate via errors.Is; the
// wrapping message names the contended home directory.
var ErrConcurrentInvocation = errors.New("another cc-port invocation is operating on the Claude home")

// ErrUnlockFailure is returned by WithLock when releasing the advisory lock
// fails on the fn-success path. The wrapping error joins the underlying
// unlock cause via %w, so errors.Is matches both this sentinel and the cause.
var ErrUnlockFailure = errors.New("release cc-port lock")

// LiveSessionsError is returned by WithLock when one or more live Claude Code
// sessions are detected before the lock is taken. Sessions carries the witness
// list; callers inspect it via errors.As. WithLock takes the lock only when the
// list is empty.
type LiveSessionsError struct {
	Sessions []tool.ActiveWriter
}

func (e *LiveSessionsError) Error() string {
	descriptors := make([]string, len(e.Sessions))
	for index, session := range e.Sessions {
		descriptors[index] = fmt.Sprintf("pid=%d cwd=%q", session.Pid, session.Cwd)
	}
	return fmt.Sprintf(
		"refusing to run: %d live Claude Code session(s) detected: [%s]",
		len(e.Sessions),
		strings.Join(descriptors, "; "),
	)
}

// WithLock runs witness before acquiring the advisory lock.
func WithLock(
	lockPath string,
	witness func() ([]tool.ActiveWriter, error),
	fn func() error,
) (returnErr error) {
	if witness == nil {
		return fmt.Errorf("witness is required")
	}
	active, err := witness()
	if err != nil {
		return fmt.Errorf("scan active writers: %w", err)
	}
	if len(active) > 0 {
		return &LiveSessionsError{Sessions: active}
	}

	lockDir := filepath.Dir(lockPath)
	if err := os.MkdirAll(lockDir, 0o750); err != nil {
		return fmt.Errorf("ensure lock directory exists: %w", err)
	}

	fileLock := flock.New(lockPath)

	ok, err := fileLock.TryLock()
	if err != nil {
		return fmt.Errorf("acquire cc-port lock: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrConcurrentInvocation, lockDir)
	}

	defer func() {
		unlockErr := unlockFn(fileLock)
		// Cleanup runs unconditionally: unlink is orthogonal to flock state, and
		// leaving the file on an unlock error would just re-accumulate stubs.
		removeErr := os.Remove(lockPath)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		if returnErr != nil {
			return
		}
		if unlockErr != nil {
			returnErr = fmt.Errorf("%w: %w", ErrUnlockFailure, unlockErr)
			return
		}
		if removeErr != nil {
			returnErr = fmt.Errorf("remove cc-port lock file: %w", removeErr)
		}
	}()

	return fn()
}
