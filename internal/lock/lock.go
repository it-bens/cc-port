//go:build darwin || linux

// Package lock guards ~/.claude against concurrent cc-port runs and live
// Claude Code sessions.
//
// The only public entry point is WithLock. The advisory lock itself is
// taken via github.com/gofrs/flock; the live-session check reads
// ~/.claude/sessions/*.json and signals PID 0 to probe liveness.
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gofrs/flock"

	"github.com/it-bens/cc-port/internal/claude"
)

// LockFileName is the name of the advisory-lock file cc-port creates inside
// the Claude Code home directory.
const LockFileName = ".cc-port.lock"

// unlockFn is the function used to release the advisory lock. Tests swap
// it to simulate a release-time failure; production always uses
// (*flock.Flock).Unlock.
var unlockFn = (*flock.Flock).Unlock

// WithLock runs the live-session check, acquires ~/.claude/.cc-port.lock
// via flock, calls fn with the lock held, and releases the lock on every
// exit path — including a panic inside fn that a caller recovers.
//
// Error precedence:
//   - If the live-session check finds a running Claude Code process, the
//     function returns a descriptive error without taking the lock and
//     without invoking fn.
//   - If another cc-port invocation holds the lock, the function returns
//     a "another cc-port invocation is operating" error verbatim from
//     the previous implementation.
//   - If fn returns a non-nil error, that error is returned; the
//     deferred release still runs, and its error (if any) is dropped on
//     this path because the caller's operational error takes
//     precedence over lock-cleanup noise.
//   - If fn returns nil, any deferred release error surfaces wrapped
//     as "release cc-port lock: %w".
func WithLock(claudeHome *claude.Home, fn func() error) (returnErr error) {
	activeSessions, err := findActiveSessions(claudeHome)
	if err != nil {
		return fmt.Errorf("scan active sessions: %w", err)
	}
	if len(activeSessions) > 0 {
		return fmt.Errorf(
			"refusing to run: %d live Claude Code session(s) detected — [%s]",
			len(activeSessions),
			strings.Join(activeSessions, "; "),
		)
	}

	if err := os.MkdirAll(claudeHome.Dir, 0o750); err != nil {
		return fmt.Errorf("ensure claude home exists: %w", err)
	}

	lockPath := filepath.Join(claudeHome.Dir, LockFileName)
	fileLock := flock.New(lockPath)

	ok, err := fileLock.TryLock()
	if err != nil {
		return fmt.Errorf("acquire cc-port lock: %w", err)
	}
	if !ok {
		return fmt.Errorf("another cc-port invocation is operating on %s", claudeHome.Dir)
	}

	defer func() {
		if unlockErr := unlockFn(fileLock); unlockErr != nil && returnErr == nil {
			returnErr = fmt.Errorf("release cc-port lock: %w", unlockErr)
		}
	}()

	return fn()
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
