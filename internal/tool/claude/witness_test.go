package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

func TestFindActiveReportsLiveSession(t *testing.T) {
	home := newWitnessHome(t)
	writeWitnessSession(t, home, "live", 42)
	workspace := NewWorkspaceForTest(home, func(string) string { return "" }, func(pid int) bool { return pid == 42 }, time.Now)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, 42, active[0].Pid)
	assert.Equal(t, "/test/project", active[0].Cwd)
}

func TestFindActiveOmitsDeadSession(t *testing.T) {
	home := newWitnessHome(t)
	writeWitnessSession(t, home, "stale", 42)
	workspace := NewWorkspaceForTest(home, func(string) string { return "" }, func(int) bool { return false }, time.Now)

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.Empty(t, active)
}

func TestFindActiveRefusesUnparseableSession(t *testing.T) {
	home := newWitnessHome(t)
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), "torn.json"), []byte(`{"cwd":`), 0o600))
	workspace := NewWorkspaceForTest(home, func(string) string { return "" }, func(int) bool { return false }, time.Now)

	active, err := workspace.ActiveWriters()

	require.Error(t, err)
	assert.Nil(t, active)
	assert.ErrorIs(t, err, tool.ErrNoWitness)
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
