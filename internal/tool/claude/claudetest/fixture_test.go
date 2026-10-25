package claudetest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool/claude/claudetest"
)

func TestSetupFixture(t *testing.T) {
	home := claudetest.SetupFixture(t)

	info, err := os.Stat(home.Dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	configBytes, err := os.ReadFile(home.ConfigFile)
	require.NoError(t, err)
	var parsedConfig map[string]any
	require.NoError(t, json.Unmarshal(configBytes, &parsedConfig),
		"fixture .claude.json must be valid JSON")

	projectDir := filepath.Join(home.Dir, "projects", "-Users-test-Projects-myproject")
	_, err = os.Stat(projectDir)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(home.Dir, "history.jsonl"))
	require.NoError(t, err)
}

// dirExists reports whether path names an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func TestRunWithIsolatedHome_RestoreResetsHome(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("HOME", "/Users/test/original-home")

	var markerDuringRun bool
	exitCode := claudetest.RunWithIsolatedHome(func() int {
		markerDuringRun = dirExists(filepath.Join(os.Getenv("HOME"), ".claude"))
		return 0
	})

	assert.True(t, markerDuringRun, "HOME must point at an isolated home holding .claude while run executes")
	assert.Equal(t, 0, exitCode)
	assert.Equal(t, "/Users/test/original-home", os.Getenv("HOME"))
}

func TestRunWithIsolatedHome_RemovesIsolatedHome(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("HOME", "/Users/test/original-home")

	var isolatedHome string
	var existedDuringRun bool
	exitCode := claudetest.RunWithIsolatedHome(func() int {
		isolatedHome = os.Getenv("HOME")
		existedDuringRun = dirExists(isolatedHome)
		return 0
	})

	require.True(t, existedDuringRun, "the isolated home must exist while run executes")
	assert.Equal(t, 0, exitCode)
	assert.False(t, dirExists(isolatedHome), "the isolated home must be removed after run returns")
}

func TestRunWithIsolatedHome_LeavesUnsetHomeUnset(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	// t.Setenv registers the cleanup that brings the caller's HOME back after
	// the unset.
	t.Setenv("HOME", "")
	require.NoError(t, os.Unsetenv("HOME"))

	exitCode := claudetest.RunWithIsolatedHome(func() int { return 0 })

	_, isSet := os.LookupEnv("HOME")
	assert.Equal(t, 0, exitCode)
	assert.False(t, isSet)
}

func TestFixtureProjectPath_StableValue(t *testing.T) {
	assert.Equal(t, "/Users/test/Projects/myproject", claudetest.FixtureProjectPath())
}
