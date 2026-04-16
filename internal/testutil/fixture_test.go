package testutil_test

import (
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

	_, err = os.Stat(home.ConfigFile)
	require.NoError(t, err)

	projectDir := filepath.Join(home.Dir, "projects", "-Users-test-Projects-myproject")
	_, err = os.Stat(projectDir)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(projectDir, "sessions-index.json"))
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(home.Dir, "history.jsonl"))
	require.NoError(t, err)
}
