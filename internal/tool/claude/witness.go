package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/it-bens/cc-port/internal/tool"
)

// procStartKey is the session-file key holding the writer process's start time.
// Claude Code writes it as `ps -o lstart=` output in UTC; older versions omit
// the key.
const procStartKey = "procStart"

// FindActive returns liveness evidence from Claude session files. A live pid is
// not on its own evidence of an active writer: the OS reuses pids, so a stale
// session file whose pid now belongs to an unrelated process would otherwise
// block every apply. On macOS a file whose recorded start time disagrees with
// the live process's is skipped; Linux offers no stable start time to compare,
// so there a live pid decides.
func FindActive(claudeHome *Home, processLiveness func(int) bool, processStartTime func(int) (time.Time, error)) ([]tool.ActiveWriter, error) {
	sessionsDir := claudeHome.SessionsDir()
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: read sessions directory: %w", tool.ErrNoWitness, err)
	}

	var active []tool.ActiveWriter
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		sessionFilePath := filepath.Join(sessionsDir, entry.Name())
		data, err := os.ReadFile(sessionFilePath) //nolint:gosec // path under claudeHome
		if err != nil {
			return nil, fmt.Errorf("%w: read session file %s: %w", tool.ErrNoWitness, sessionFilePath, err)
		}
		var sessionFile SessionFile
		if err := json.Unmarshal(data, &sessionFile); err != nil {
			return nil, fmt.Errorf("%w: parse session file %s: %w", tool.ErrNoWitness, sessionFilePath, err)
		}
		if sessionFile.Pid <= 0 || !processLiveness(sessionFile.Pid) {
			continue
		}
		if !writerStartedAtMatches(sessionFile, sessionFile.Pid, processStartTime) {
			continue
		}
		active = append(active, tool.ActiveWriter{Pid: sessionFile.Pid, Cwd: sessionFile.Cwd})
	}
	return active, nil
}

// writerStartedAtMatches reports whether the process behind pid can be the
// writer that produced sessionFile. Both refusals to decide fall back to
// liveness alone, and that fallback is load-bearing rather than convenient: a
// file with no usable procStart is what a pre-procStart Claude Code wrote, and
// a start time that cannot be read means the process exited between the two
// probes, or that the platform has no live start time to read at all (Linux).
// Neither may turn the refusal into tool.ErrNoWitness, so neither returns an
// error.
func writerStartedAtMatches(sessionFile SessionFile, pid int, processStartTime func(int) (time.Time, error)) bool {
	recorded, usable := parseProcStart(sessionFile)
	if !usable {
		return true
	}
	live, err := processStartTime(pid)
	if err != nil {
		return true
	}
	return withinOneSecond(recorded, live)
}

// parseProcStart reads the writer's recorded start time out of the session
// file's unknown-key bag. It reports false when the key is absent, holds a
// non-string, or does not parse in the ctime layout Claude Code emits.
func parseProcStart(sessionFile SessionFile) (time.Time, bool) {
	raw, present := sessionFile.Extra[procStartKey]
	if !present {
		return time.Time{}, false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return time.Time{}, false
	}
	parsed, err := time.ParseInLocation(time.ANSIC, value, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// withinOneSecond reports whether two process start times, truncated to whole
// seconds, differ by at most one second.
func withinOneSecond(a, b time.Time) bool {
	delta := a.Truncate(time.Second).Sub(b.Truncate(time.Second))
	if delta < 0 {
		delta = -delta
	}
	return delta <= time.Second
}

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
