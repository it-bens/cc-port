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

func TestIsolateHome_RestoreResetsHomeAndRemovesIsolatedHome(t *testing.T) {
	t.Setenv("HOME", "/Users/test/original-home")
	restore, err := claudetest.IsolateHome()
	require.NoError(t, err)
	isolatedHome := os.Getenv("HOME")
	require.DirExists(t, filepath.Join(isolatedHome, ".claude"))

	err = restore()

	require.NoError(t, err)
	assert.Equal(t, "/Users/test/original-home", os.Getenv("HOME"))
	assert.NoDirExists(t, isolatedHome)
}

func TestIsolateHome_RestoreUnsetsHomeThatWasUnset(t *testing.T) {
	// t.Setenv registers the cleanup that brings the caller's HOME back after the unset.
	t.Setenv("HOME", "")
	require.NoError(t, os.Unsetenv("HOME"))
	restore, err := claudetest.IsolateHome()
	require.NoError(t, err)

	err = restore()

	require.NoError(t, err)
	_, isSet := os.LookupEnv("HOME")
	assert.False(t, isSet)
}

func TestFixtureProjectPath_StableValue(t *testing.T) {
	assert.Equal(t, "/Users/test/Projects/myproject", claudetest.FixtureProjectPath())
}
