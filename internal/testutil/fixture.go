// Package testutil provides test helpers for setting up Claude Code fixture data.
package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/fsutil"
)

// SetupFixture copies the testdata/dotclaude fixture into a temporary directory
// and returns a Home pointing to it.
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
	configData, err := os.ReadFile(sourceConfigPath) //nolint:gosec // G304: sourceConfigPath is inside t.TempDir() (test-only fixture)
	if err != nil {
		t.Fatalf("read fixture .claude.json: %v", err)
	}
	if err := os.WriteFile(configFilePath, configData, 0600); err != nil { //nolint:gosec // G306: 0600 is intentional for the test fixture's user config copy
		t.Fatalf("write config file: %v", err)
	}
	_ = os.Remove(sourceConfigPath)

	return &claude.Home{
		Dir:        claudeDir,
		ConfigFile: configFilePath,
	}
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
			t.Fatal("could not find testdata/ directory")
		}
		currentDir = parentDir
	}
}
