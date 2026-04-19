package testutil_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
