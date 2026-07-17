package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindActiveReportsLiveSession(t *testing.T) {
	home := newWitnessHome(t)
	writeWitnessSession(t, home, "live", os.Getpid())

	active, err := FindActive(home)

	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, os.Getpid(), active[0].Pid)
	assert.Equal(t, "/test/project", active[0].Cwd)
}

func TestFindActiveOmitsDeadSession(t *testing.T) {
	home := newWitnessHome(t)
	writeWitnessSession(t, home, "stale", 2_000_000_001)

	active, err := FindActive(home)

	require.NoError(t, err)
	assert.Empty(t, active)
}

func newWitnessHome(t *testing.T) *Home {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "dotclaude")
	require.NoError(t, os.MkdirAll(directory, 0o750))
	return &Home{Dir: directory, ConfigFile: directory + ".json"}
}

func writeWitnessSession(t *testing.T, home *Home, name string, pid int) {
	t.Helper()

	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	data, err := json.Marshal(SessionFile{Cwd: "/test/project", Pid: pid})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), name+".json"), data, 0o600))
}
