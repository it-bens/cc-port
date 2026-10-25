// Package claudetest provides test helpers for setting up Claude Code fixture data.
package claudetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/fsutil"
	"github.com/it-bens/cc-port/internal/testmain"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// deadSessionPID exceeds the Linux/macOS PID ceiling but stays below int32 max, so Kill(pid, 0) returns ESRCH.
const deadSessionPID = 2_000_000_001

// isolateHome redirects HOME to a newly created directory with only an empty
// .claude marker. The marker keeps legacy Claude-only command tests selected
// while ensuring Codex is never detected; fixture homes are still passed by
// explicit flags. The returned restore sets HOME back to its previous value,
// or unsets it when it had none, removes the directory, and returns any error
// from either step.
func isolateHome() (restore func() error, err error) {
	dir, err := os.MkdirTemp("", "cc-port-test-home-")
	if err != nil {
		return nil, fmt.Errorf("create isolated home: %w", err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0o700); err != nil {
		return nil, testmain.RemoveAfterFailure(dir, fmt.Errorf("create .claude marker: %w", err))
	}

	previous, hadPrevious := os.LookupEnv("HOME")
	if err := os.Setenv("HOME", dir); err != nil {
		return nil, testmain.RemoveAfterFailure(dir, fmt.Errorf("point HOME at isolated home: %w", err))
	}

	return func() error {
		var envErr error
		if hadPrevious {
			envErr = os.Setenv("HOME", previous)
		} else {
			envErr = os.Unsetenv("HOME")
		}
		if envErr != nil {
			envErr = fmt.Errorf("restore HOME: %w", envErr)
		}
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			return errors.Join(envErr, fmt.Errorf("remove isolated home: %w", removeErr))
		}
		return envErr
	}, nil
}

// NewWorkspace returns a Workspace bound to home with the adapter's default
// environment, process-liveness, and process-start-time seams.
func NewWorkspace(home *claude.Home) *claude.Workspace {
	return claude.NewWorkspace(home, os.Getenv, claude.ProcessAlive, claude.ProcessStart)
}

// SetupFixture stages testdata/dotclaude under t.TempDir(), sanitizes session PIDs, and returns a Home pointing to it.
func SetupFixture(t *testing.T) *claude.Home {
	t.Helper()

	home, err := StageFixture(t.TempDir())
	require.NoError(t, err)
	return home
}

// StageFixture stages testdata/dotclaude under dir, moves the tree's config
// file beside it and sanitizes session PIDs, returning a Home pointing at the
// staged copy. It takes no testing.TB so a package can build one read-only
// fixture before running any test.
func StageFixture(dir string) (*claude.Home, error) {
	claudeDir := filepath.Join(dir, "dotclaude")
	configFilePath := filepath.Join(dir, "dotclaude.json")

	fixtureDir, err := findFixtureDir()
	if err != nil {
		return nil, err
	}

	if err := fsutil.CopyDir(context.Background(), filepath.Join(fixtureDir, "dotclaude"), claudeDir, nil); err != nil {
		return nil, fmt.Errorf("copy fixture directory: %w", err)
	}

	sourceConfigPath := filepath.Join(claudeDir, ".claude.json")
	configData, err := os.ReadFile(sourceConfigPath) //nolint:gosec // G304: path inside the caller's staging directory
	if err != nil {
		return nil, fmt.Errorf("read fixture .claude.json: %w", err)
	}
	//nolint:gosec // G306: 0600 intentional for fixture
	if err := os.WriteFile(configFilePath, configData, 0o600); err != nil {
		return nil, fmt.Errorf("write config file: %w", err)
	}
	if err := os.Remove(sourceConfigPath); err != nil {
		return nil, fmt.Errorf("remove staged .claude.json: %w", err)
	}

	if err := replaceSessionPIDs(filepath.Join(claudeDir, "sessions"), deadSessionPID); err != nil {
		return nil, fmt.Errorf("sanitize fixture session PIDs: %w", err)
	}

	return &claude.Home{
		Dir:        claudeDir,
		ConfigFile: configFilePath,
	}, nil
}

// sharedHome holds the fixture installSharedFixture staged. A package's
// TestMain installs it before its tests start, so tests only read it,
// including when they run in parallel.
var sharedHome *claude.Home

// installSharedFixture stages the fixture once into a fresh temporary
// directory and records it for SharedFixture. RunWithSharedFixture calls it
// before the tests start; the returned cleanup removes the staged directory.
func installSharedFixture() (cleanup func() error, err error) {
	dir, err := os.MkdirTemp("", "cc-port-claude-fixture-")
	if err != nil {
		return nil, fmt.Errorf("create shared fixture directory: %w", err)
	}

	home, err := StageFixture(dir)
	if err != nil {
		return nil, testmain.RemoveAfterFailure(dir, err)
	}
	sharedHome = home

	return func() error { return os.RemoveAll(dir) }, nil
}

// RunWithSharedFixture installs the shared fixture, runs run, and removes the
// fixture, returning the exit code for TestMain to pass to os.Exit.
func RunWithSharedFixture(run func() int) int {
	return testmain.Run(installSharedFixture, run)
}

// RunWithIsolatedHome points HOME at an isolated directory for the duration of
// run and restores it afterwards, returning run's exit code for TestMain to
// pass to os.Exit.
func RunWithIsolatedHome(run func() int) int {
	return testmain.Run(isolateHome, run)
}

// SharedFixture returns the read-only fixture installSharedFixture staged. Only
// tests that never write under the home may use it; a writing test stages its
// own copy with SetupFixture. A missing installation fails the test rather
// than handing back an empty home.
func SharedFixture(t testing.TB) *claude.Home {
	t.Helper()

	if sharedHome == nil {
		t.Fatalf("claudetest.SharedFixture: no shared fixture installed; call claudetest.RunWithSharedFixture from TestMain")
	}
	return sharedHome
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
		data, err := os.ReadFile(sessionFilePath) //nolint:gosec // G304: path inside the fixture staging directory
		if err != nil {
			return err
		}
		var sessionFile claude.SessionFile
		if err := json.Unmarshal(data, &sessionFile); err != nil {
			return fmt.Errorf("parse fixture session file %s: %w", entry.Name(), err)
		}
		sessionFile.Pid = deadPID
		updated, err := json.Marshal(sessionFile)
		if err != nil {
			return err
		}
		if err := os.WriteFile(sessionFilePath, updated, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// FixtureProjectPath returns the canonical project path the fixture tree is
// keyed on. Tests that drive a cmd through the happy path against
// SetupFixture's home pass this as the project argument.
func FixtureProjectPath() string {
	return "/Users/test/Projects/myproject"
}

func findFixtureDir() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	// Walk upward looking for a testdata/ that contains our dotclaude/ fixture.
	// A bare testdata/ check would match a package-local fuzz corpus (e.g.
	// internal/tool/claude/testdata/fuzz/) and stop before the repo-root testdata/,
	// leading to a confusing "no such file or directory" on dotclaude.
	for {
		candidate := filepath.Join(currentDir, "testdata")
		if info, err := os.Stat(filepath.Join(candidate, "dotclaude")); err == nil && info.IsDir() {
			return candidate, nil
		}
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", fmt.Errorf("could not find testdata/dotclaude/ directory from %s", currentDir)
		}
		currentDir = parentDir
	}
}
