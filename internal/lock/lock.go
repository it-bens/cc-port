//go:build darwin || linux

// Package lock guards ~/.claude against concurrent cc-port runs and live
// Claude Code sessions.
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

// FileName is the name of the advisory-lock file cc-port creates inside
// the Claude Code home directory.
const FileName = ".cc-port.lock"

// unlockFn is the function used to release the advisory lock. Tests swap
// it to simulate a release-time failure; production always uses
// (*flock.Flock).Unlock.
var unlockFn = (*flock.Flock).Unlock

// WithLock runs the live-session check before taking the lock, ensuring
// no Claude Code session is active before fn is called.
func WithLock(claudeHome *claude.Home, fn func() error) (returnErr error) {
	active, err := FindActive(claudeHome)
	if err != nil {
		return fmt.Errorf("scan active sessions: %w", err)
	}
	if len(active) > 0 {
		descriptors := make([]string, len(active))
		for index, session := range active {
			descriptors[index] = fmt.Sprintf("pid=%d cwd=%q", session.Pid, session.Cwd)
		}
		return fmt.Errorf(
			"refusing to run: %d live Claude Code session(s) detected: [%s]",
			len(active),
			strings.Join(descriptors, "; "),
		)
	}

	if err := os.MkdirAll(claudeHome.Dir, 0o750); err != nil {
		return fmt.Errorf("ensure claude home exists: %w", err)
	}

	lockPath := filepath.Join(claudeHome.Dir, FileName)
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

// ActiveSession describes one live Claude Code process identified from
// ~/.claude/sessions/<pid>.json.
type ActiveSession struct {
	Pid int
	Cwd string
}

// FindActive returns one ActiveSession per ~/.claude/sessions/*.json
// file whose recorded PID is alive on the host. An empty or missing
// sessions directory produces a nil slice and no error so fresh
// installations pass through cleanly. Callers that want to refuse on
// any live session should test len(result) > 0; callers that want to
// filter by project pass the cwd through a downstream equality check.
func FindActive(claudeHome *claude.Home) ([]ActiveSession, error) {
	sessionsDir := claudeHome.SessionsDir()
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions directory: %w", err)
	}

	var active []ActiveSession
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
			// Unknown / future schema; skip rather than block.
			continue
		}
		if sessionFile.Pid <= 0 {
			continue
		}
		if !processAlive(sessionFile.Pid) {
			continue
		}
		active = append(active, ActiveSession{Pid: sessionFile.Pid, Cwd: sessionFile.Cwd})
	}
	return active, nil
}

// processAlive reports whether a process with the given PID is currently
// running on the host. Both "exists and signalable" and "exists but owned
// by another user" count as alive; only "no such process" counts as dead.
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
