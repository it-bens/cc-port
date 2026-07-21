package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/it-bens/cc-port/internal/tool"
)

// FindActive returns liveness evidence from Claude session files.
func FindActive(claudeHome *Home, processLiveness func(int) bool) ([]tool.ActiveWriter, error) {
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
		active = append(active, tool.ActiveWriter{Pid: sessionFile.Pid, Cwd: sessionFile.Cwd})
	}
	return active, nil
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
