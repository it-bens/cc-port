package testutil_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/testutil"
)

func TestSetupFixture(t *testing.T) {
	home := testutil.SetupFixture(t)

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

func TestFixtureProjectPath_StableValue(t *testing.T) {
	assert.Equal(t, "/Users/test/Projects/myproject", testutil.FixtureProjectPath())
}

func TestWriteFixtureArchive_ProducesValidArchive(t *testing.T) {
	path := testutil.WriteFixtureArchive(t)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Positive(t, info.Size())

	// Manifest read must succeed; archive is a valid cc-port export.
	file, err := os.Open(path) //nolint:gosec // G304: path returned from helper rooted in t.TempDir
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	metadata, err := manifest.ReadManifestFromZip(file, info.Size())
	require.NoError(t, err)
	assert.NotEmpty(t, metadata.Export.Categories)
}
