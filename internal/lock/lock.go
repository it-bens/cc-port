// Package lock guards ~/.claude against concurrent cc-port runs and live
// Claude Code sessions.
//
// The only public entry point is WithLock. The underlying acquire/release
// helpers are deliberately unexported so the lock-first contract for
// mutating commands is structural (a new caller cannot forget to wrap their
// body) rather than conventional.
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/it-bens/cc-port/internal/claude"
)

// LockFileName is the name of the advisory-lock file cc-port creates inside
// the Claude Code home directory.
const LockFileName = ".cc-port.lock"

// closeLockFile is the function release uses to close the lock file. Tests
// swap it to simulate a release-time failure; production always uses
// (*os.File).Close.
var closeLockFile = (*os.File).Close

// lock is an acquired exclusive advisory lock over a Claude Code home
// directory. The zero value is not usable; obtain one via acquire. The type
// is deliberately unexported so callers cannot take the handle outside the
// lifecycle WithLock owns.
type lock struct {
	file *os.File
}

// release closes the backing file descriptor, which releases the flock.
// Subsequent calls are no-ops.
func (lockHandle *lock) release() error {
	if lockHandle == nil || lockHandle.file == nil {
		return nil
	}
	err := closeLockFile(lockHandle.file)
	lockHandle.file = nil
	return err
}

// acquire verifies that no concurrent cc-port invocation or live Claude
// Code session is writing to claudeHome, then takes an exclusive advisory
// lock over it.
//
// Any ~/.claude/sessions/*.json entry whose PID is alive on the host causes
// acquire to fail without taking the lock. A second cc-port holding the
// advisory lock likewise causes acquire to fail. The kernel releases the
// lock when the owning process exits.
//
// acquire is unexported on purpose: callers go through WithLock, which
// guarantees release runs on every exit path. Exporting acquire would let a
// future mutating command skip the release and leak the lock until process
// exit, reintroducing the class of bug WithLock exists to prevent.
func acquire(claudeHome *claude.Home) (*lock, error) {
	activeSessions, err := findActiveSessions(claudeHome)
	if err != nil {
		return nil, fmt.Errorf("scan active sessions: %w", err)
	}
	if len(activeSessions) > 0 {
		return nil, fmt.Errorf(
			"refusing to run: %d live Claude Code session(s) detected — [%s]",
			len(activeSessions),
			strings.Join(activeSessions, "; "),
		)
	}

	if err := os.MkdirAll(claudeHome.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("ensure claude home exists: %w", err)
	}

	lockPath := filepath.Join(claudeHome.Dir, LockFileName)
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // G304: path under claudeHome
	if err != nil {
		return nil, fmt.Errorf("open cc-port lock file: %w", err)
	}

	//nolint:gosec // G115: file descriptor values always fit into int on supported platforms
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another cc-port invocation is operating on %s", claudeHome.Dir)
		}
		return nil, fmt.Errorf("acquire cc-port lock: %w", err)
	}

	return &lock{file: file}, nil
}

// WithLock acquires ~/.claude/.cc-port.lock, runs the live-session check,
// calls fn with the lock held, and releases the lock regardless of fn's
// outcome.
//
// Error precedence:
//   - If acquire fails (contention or live-session abort), the error is
//     returned verbatim and fn is not invoked.
//   - If fn returns a non-nil error, that error is returned; release still
//     runs, but its error is dropped on this path because the caller's
//     operational error takes precedence over lock-cleanup noise.
//   - If fn returns nil, release's error (if any) surfaces. Earlier
//     defer-based callers silently swallowed this error; it is now
//     observable on the success path.
func WithLock(claudeHome *claude.Home, fn func() error) error {
	lockHandle, err := acquire(claudeHome)
	if err != nil {
		return err
	}
	fnErr := fn()
	releaseErr := lockHandle.release()
	if fnErr != nil {
		return fnErr
	}
	return releaseErr
}

// findActiveSessions returns one descriptor per ~/.claude/sessions/*.json
// file whose recorded PID is alive on the host. Each descriptor has the
// form "pid <pid> cwd <cwd>" so the abort message is actionable.
func findActiveSessions(claudeHome *claude.Home) ([]string, error) {
	sessionsDir := claudeHome.SessionsDir()
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions directory: %w", err)
	}

	var active []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		sessionFilePath := filepath.Join(sessionsDir, entry.Name())
		data, err := os.ReadFile(sessionFilePath) //nolint:gosec // path under claudeHome
		if err != nil {
			return nil, fmt.Errorf("read session file %s: %w", sessionFilePath, err)
		}
		var sessionFile claude.SessionFile
		if err := json.Unmarshal(data, &sessionFile); err != nil {
			// Unknown / future schema — skip rather than block.
			continue
		}
		if sessionFile.Pid <= 0 {
			continue
		}
		if !processAlive(sessionFile.Pid) {
			continue
		}
		active = append(active, fmt.Sprintf("pid %d cwd %s", sessionFile.Pid, sessionFile.Cwd))
	}
	return active, nil
}

// processAlive reports whether a process with the given PID is currently
// running on the host. Both "exists and signalable" and "exists but owned
// by another user" count as alive; only "no such process" counts as dead.
// The caller supplies a positive PID.
func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
