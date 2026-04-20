// Package testutil provides test helpers for setting up Claude Code fixture data.
package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/fsutil"
)

// deadSessionPID exceeds the Linux/macOS PID ceiling but stays below int32 max, so Kill(pid, 0) returns ESRCH.
const deadSessionPID = 2_000_000_001

// SetupFixture stages testdata/dotclaude under t.TempDir(), sanitises session PIDs, and returns a Home pointing to it.
func SetupFixture(t *testing.T) *claude.Home {
	t.Helper()

	temporaryDir := t.TempDir()
	claudeDir := filepath.Join(temporaryDir, "dotclaude")
	configFilePath := filepath.Join(temporaryDir, "dotclaude.json")

	fixtureDir := findFixtureDir(t)

	if err := fsutil.CopyDir(filepath.Join(fixtureDir, "dotclaude"), claudeDir); err != nil {
		t.Fatalf("copy fixture directory: %v", err)
	}

	sourceConfigPath := filepath.Join(claudeDir, ".claude.json")
	configData, err := os.ReadFile(sourceConfigPath) //nolint:gosec // G304: path inside t.TempDir()
	if err != nil {
		t.Fatalf("read fixture .claude.json: %v", err)
	}
	//nolint:gosec // G306: 0600 intentional for fixture
	if err := os.WriteFile(configFilePath, configData, 0600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	_ = os.Remove(sourceConfigPath)

	if err := replaceSessionPIDs(filepath.Join(claudeDir, "sessions"), deadSessionPID); err != nil {
		t.Fatalf("sanitize fixture session PIDs: %v", err)
	}

	return &claude.Home{
		Dir:        claudeDir,
		ConfigFile: configFilePath,
	}
}

// replaceSessionPIDs rewrites "pid" in every sessions/*.json to deadPID.
// Fixture PIDs (12345, 99999, …) can coincidentally match a live process and trip the lock guard.
func replaceSessionPIDs(sessionsDir string, deadPID int) error {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		sessionFilePath := filepath.Join(sessionsDir, entry.Name())
		data, err := os.ReadFile(sessionFilePath) //nolint:gosec // G304: path inside t.TempDir()
		if err != nil {
			return err
		}
		var sessionFile claude.SessionFile
		if err := json.Unmarshal(data, &sessionFile); err != nil {
			continue
		}
		sessionFile.Pid = deadPID
		updated, err := json.Marshal(sessionFile)
		if err != nil {
			return err
		}
		if err := os.WriteFile(sessionFilePath, updated, 0600); err != nil {
			return err
		}
	}
	return nil
}

func findFixtureDir(t *testing.T) string {
	t.Helper()

	currentDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	for {
		candidate := filepath.Join(currentDir, "testdata")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			// No testdata/ means the test ran outside the repository tree; this is a programmer error.
			t.Fatal("could not find testdata/ directory")
		}
		currentDir = parentDir
	}
}
