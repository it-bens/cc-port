// Package lock provides concurrency guards that prevent cc-port from running
// while another cc-port invocation or a live Claude Code session is writing
// to the same ~/.claude directory.
//
// Two distinct races are guarded:
//
//  1. cc-port vs another cc-port — an exclusive advisory lock is taken on
//     ~/.claude/.cc-port.lock via flock(2). A second invocation fails
//     immediately instead of racing on the shared files.
//  2. cc-port vs a live Claude Code session — every ~/.claude/sessions/*.json
//     file is inspected; if its PID is alive on the host, cc-port aborts.
//     Stale session files left behind by crashed Claude Code runs are ignored
//     because the recorded PID will not be alive.
//
// Release MUST be called on every exit path — the kernel releases the flock
// when the file descriptor is closed, but callers should still Close
// explicitly for timely cleanup and deterministic error reporting.
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

// Lock is an acquired exclusive advisory lock over a Claude Code home
// directory. The zero value is not usable; obtain one via Acquire.
type Lock struct {
	file *os.File
}

// Release closes the backing file descriptor, which releases the flock.
// Subsequent calls are no-ops.
func (lockHandle *Lock) Release() error {
	if lockHandle == nil || lockHandle.file == nil {
		return nil
	}
	err := lockHandle.file.Close()
	lockHandle.file = nil
	return err
}

// Acquire verifies that no concurrent cc-port invocation or live Claude Code
// session is writing to claudeHome, then takes an exclusive advisory lock
// over it.
//
// Any ~/.claude/sessions/*.json entry whose PID is alive on the host causes
// Acquire to fail without taking the lock. A second cc-port holding the
// advisory lock likewise causes Acquire to fail. The kernel releases the
// lock when the owning process exits.
//
// Callers MUST Release the returned Lock on every exit path.
func Acquire(claudeHome *claude.Home) (*Lock, error) {
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

	return &Lock{file: file}, nil
}

// findActiveSessions returns one descriptor per ~/.claude/sessions/*.json
// file whose recorded PID is alive on the host. Each descriptor has the form
// "pid <pid> cwd <cwd>" so the abort message is actionable.
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
// running on the host. Both "exists and signalable" and "exists but owned by
// another user" count as alive; only "no such process" counts as dead. The
// caller supplies a positive PID.
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
