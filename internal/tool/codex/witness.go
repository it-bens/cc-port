package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gofrs/flock"

	"github.com/it-bens/cc-port/internal/tool"
)

// Rollout root subdirectories (rollout/src/lib.rs:21-22).
const (
	sessionsSubdir         = "sessions"
	archivedSessionsSubdir = "archived_sessions"
)

const (
	appServerDaemonSubdir   = "app-server-daemon"
	appServerPIDFileName    = "app-server.pid" // app-server-daemon/src/lib.rs:31
	daemonLockFileName      = "daemon.lock"    // app-server-daemon/src/lib.rs:33
	tmpSubdir               = ".tmp"
	compressionLockFileName = "rollout-compression.lock" // rollout/src/compression.rs:257
)

// freshnessWindow is how recently a rollout or WAL/SHM sibling must have
// been modified to count as live-writer evidence (spec §6.4 evidence 2).
const freshnessWindow = 120 * time.Second

// compressionMarkerStaleAfter mirrors Codex's own RUN_MARKER_STALE_AFTER
// (rollout/src/compression.rs:254): a marker at or past this age is
// reclaimed by Codex's own compression worker, so it is never evidence
// regardless of its embedded pid.
const compressionMarkerStaleAfter = 6 * time.Hour

// codexProcessNames are the binary names a running Codex install can
// appear under in the process table, per the workspace Cargo.toml
// `[[bin]]` declarations for the CLI/TUI (cli/Cargo.toml, tui/Cargo.toml)
// and the app-server the daemon manages (app-server/Cargo.toml).
var codexProcessNames = map[string]bool{
	"codex":            true,
	"codex-tui":        true,
	"codex-app-server": true,
}

// ActiveWriters implements tool.Workspace. It gathers liveness evidence in
// the order documented at spec §6.4. Every source runs regardless of an
// earlier source's outcome, so a dry-run can report every signal at once;
// but a source that cannot be read makes the whole call fail, wrapping
// tool.ErrNoWitness, which blocks mutation exactly like a positive result.
func (workspace *Workspace) ActiveWriters() ([]tool.ActiveWriter, error) {
	var active []tool.ActiveWriter
	var readErrs []error

	collect := func(writers []tool.ActiveWriter, err error) {
		if err != nil {
			readErrs = append(readErrs, err)
			return
		}
		active = append(active, writers...)
	}

	collect(workspace.processTableWitness())
	collect(workspace.freshnessWitness())
	collect(workspace.daemonWitness())
	collect(workspace.compressionLockWitness())
	collect(workspace.busyProbeWitness())

	if len(readErrs) > 0 {
		return nil, fmt.Errorf("%w: %w", tool.ErrNoWitness, errors.Join(readErrs...))
	}
	return active, nil
}

// processTableWitness is evidence source 1: a running codex process is the
// primary signal, since a plain `codex`/`codex exec` run holds a database
// open with no daemon directory and no marker file.
func (workspace *Workspace) processTableWitness() ([]tool.ActiveWriter, error) {
	processes, err := workspace.listProcesses()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	var active []tool.ActiveWriter
	for _, process := range processes {
		if codexProcessNames[filepath.Base(process.Name)] {
			active = append(active, tool.ActiveWriter{Pid: process.PID})
		}
	}
	return active, nil
}

// freshnessWitness is evidence source 2: any rollout under either sessions
// root, or any -wal/-shm sibling of a discovered database, modified within
// freshnessWindow.
func (workspace *Workspace) freshnessWitness() ([]tool.ActiveWriter, error) {
	cutoff := workspace.now().Add(-freshnessWindow)
	rollouts, err := discoverRolloutFiles(workspace.home)
	if err != nil {
		return nil, err
	}
	fresh, err := anyRolloutModifiedSince(rollouts, cutoff)
	if err != nil {
		return nil, err
	}

	databases, err := workspace.allDatabasePaths()
	if err != nil {
		return nil, err
	}
	for _, database := range databases {
		for _, suffix := range []string{walSuffix, shmSuffix} {
			info, statErr := os.Stat(database + suffix)
			if statErr != nil {
				if errors.Is(statErr, fs.ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("stat %s: %w", database+suffix, statErr)
			}
			if info.ModTime().After(cutoff) {
				fresh = true
			}
		}
	}

	if fresh {
		return []tool.ActiveWriter{{}}, nil
	}
	return nil, nil
}

func anyRolloutModifiedSince(paths []string, cutoff time.Time) (bool, error) {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			// A rollout discovered a moment ago can vanish before this stat
			// when Codex archives it (rollout-*.jsonl renamed to .jsonl.zst);
			// a gone file is not readable freshness evidence, so skip it.
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return false, fmt.Errorf("stat %s: %w", path, err)
		}
		if info.ModTime().After(cutoff) {
			return true, nil
		}
	}
	return false, nil
}

// daemonWitness is evidence source 3: a live PID in
// app-server-daemon/app-server.pid, or a held flock on
// app-server-daemon/daemon.lock (app-server-daemon/src/lib.rs:31,33).
func (workspace *Workspace) daemonWitness() ([]tool.ActiveWriter, error) {
	daemonDir := filepath.Join(workspace.home.Dir, appServerDaemonSubdir)

	var active []tool.ActiveWriter

	pidWriter, err := pidFileWitness(filepath.Join(daemonDir, appServerPIDFileName), workspace.pidAlive)
	if err != nil {
		return nil, err
	}
	if pidWriter != nil {
		active = append(active, *pidWriter)
	}

	lockHeld, err := flockCurrentlyHeld(filepath.Join(daemonDir, daemonLockFileName))
	if err != nil {
		return nil, err
	}
	if lockHeld {
		active = append(active, tool.ActiveWriter{})
	}

	return active, nil
}

// pidRecord mirrors app-server-daemon/src/backend/pid.rs's PidRecord: the
// pid file holds a JSON object, not a bare integer. This witness accepts a
// live PID without checking process start time, so PID reuse can produce a
// false positive.
type pidRecord struct {
	PID int `json:"pid"`
}

// pidFileWitness reads path (app-server.pid) and returns evidence when it
// holds a live PID. A missing or empty file (no reservation in progress)
// is not evidence.
func pidFileWitness(path string, pidAlive func(int) bool) (*tool.ActiveWriter, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path constructed from resolved codex home
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, nil
	}
	var record pidRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if record.PID <= 0 || !pidAlive(record.PID) {
		return nil, nil
	}
	return &tool.ActiveWriter{Pid: record.PID}, nil
}

// flockCurrentlyHeld reports whether path is currently exclusively locked
// by another process: it tries a non-blocking flock and immediately
// releases it if acquired. A missing file is not evidence.
func flockCurrentlyHeld(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	fileLock := flock.New(path)
	ok, err := fileLock.TryLock()
	if err != nil {
		return false, fmt.Errorf("probe lock %s: %w", path, err)
	}
	if !ok {
		return true, nil
	}
	if err := fileLock.Unlock(); err != nil {
		return false, fmt.Errorf("release probe lock %s: %w", path, err)
	}
	return false, nil
}

// compressionLockWitness is evidence source 4:
// $CODEX_HOME/.tmp/rollout-compression.lock counts only when its mtime is
// inside the six-hour staleness window and its embedded pid is alive; the
// marker persists after successful runs, so presence alone proves nothing
// (rollout/src/compression.rs:253-257,274-277,391-407).
func (workspace *Workspace) compressionLockWitness() ([]tool.ActiveWriter, error) {
	path := filepath.Join(workspace.home.Dir, tmpSubdir, compressionLockFileName)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if workspace.now().Sub(info.ModTime()) >= compressionMarkerStaleAfter {
		return nil, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path constructed from resolved codex home
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	pid, ok := parseCompressionMarkerPID(data)
	if !ok {
		return nil, fmt.Errorf("parse compression marker %s", path)
	}
	if !workspace.pidAlive(pid) {
		return nil, nil
	}
	return []tool.ActiveWriter{{Pid: pid}}, nil
}

// parseCompressionMarkerPID extracts the pid from a marker written by
// create_run_marker_file: "pid=<pid> started_at=<debug-formatted time>".
func parseCompressionMarkerPID(data []byte) (int, bool) {
	const prefix = "pid="
	text := string(data)
	if !strings.HasPrefix(text, prefix) {
		return 0, false
	}
	rest := text[len(prefix):]
	end := strings.IndexAny(rest, " \t\r\n")
	if end == -1 {
		end = len(rest)
	}
	pid, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return pid, true
}

// busyProbeWitness is evidence source 5: SQLITE_BUSY on a BEGIN IMMEDIATE
// probe against each discovered database, backstopping the process-table
// and freshness signals for a writer this witness otherwise cannot see.
func (workspace *Workspace) busyProbeWitness() ([]tool.ActiveWriter, error) {
	databases, err := workspace.allDatabasePaths()
	if err != nil {
		return nil, err
	}
	var active []tool.ActiveWriter
	for _, database := range databases {
		busy, probeErr := probeDatabaseBusy(database)
		if probeErr != nil {
			return nil, probeErr
		}
		if busy {
			active = append(active, tool.ActiveWriter{})
		}
	}
	return active, nil
}

// processAlive reports whether pid identifies a running process, tolerating
// a permission-denied signal (owned by another user, still alive).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
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
