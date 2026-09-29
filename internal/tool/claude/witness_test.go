package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

// witnessCwd is the cwd every session file this file writes carries.
const witnessCwd = "/test/project"

func TestFindActiveReportsLiveSession(t *testing.T) {
	home := newWitnessHome(t)
	writeWitnessSession(t, home, "live", 42)
	workspace := NewWorkspaceForTest(home, noWitnessEnv, aliveOnly(42), noStartTimeProbe(t))

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, 42, active[0].Pid)
	assert.Equal(t, witnessCwd, active[0].Cwd)
}

func TestFindActiveOmitsDeadSession(t *testing.T) {
	home := newWitnessHome(t)
	writeWitnessSession(t, home, "stale", 42)
	workspace := NewWorkspaceForTest(home, noWitnessEnv, func(int) bool { return false }, noStartTimeProbe(t))

	active, err := workspace.ActiveWriters()

	require.NoError(t, err)
	assert.Empty(t, active)
}

func TestFindActiveRefusesUnparseableSession(t *testing.T) {
	home := newWitnessHome(t)
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), "torn.json"), []byte(`{"cwd":`), 0o600))
	workspace := NewWorkspaceForTest(home, noWitnessEnv, func(int) bool { return false }, noStartTimeProbe(t))

	active, err := workspace.ActiveWriters()

	require.Error(t, err)
	assert.Nil(t, active)
	assert.ErrorIs(t, err, tool.ErrNoWitness)
}

// TestFindActiveJudgesProcStartAgreement pins when a live pid is accepted as the
// session file's writer. Agreement is truncated to whole seconds with one second
// of slack, so the kernel's sub-second start time and the `ps` rendering Claude
// Code recorded both count as matches.
func TestFindActiveJudgesProcStartAgreement(t *testing.T) {
	procStart, recorded := fixtureHostProcStart(t)

	cases := []struct {
		name         string
		live         time.Time
		readErr      error
		wantReported bool
	}{
		{name: "matching start time", live: recorded, wantReported: true},
		{name: "one second later", live: recorded.Add(time.Second), wantReported: true},
		{name: "one second earlier", live: recorded.Add(-time.Second), wantReported: true},
		{name: "sub-second skew", live: recorded.Add(900 * time.Millisecond), wantReported: true},
		{name: "two seconds apart", live: recorded.Add(2 * time.Second), wantReported: false},
		{name: "unrelated process days later", live: recorded.Add(72 * time.Hour), wantReported: false},
		{
			name:         "start time read fails",
			readErr:      errors.New("process exited between the liveness and start-time probes"),
			wantReported: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			home := newWitnessHome(t)
			writeWitnessSessionWithProcStart(t, home, "writer", 42, procStart)
			workspace := NewWorkspaceForTest(home, noWitnessEnv, aliveOnly(42), func(int) (time.Time, error) {
				return testCase.live, testCase.readErr
			})

			active, err := workspace.ActiveWriters()

			require.NoError(t, err)
			if !testCase.wantReported {
				assert.Empty(t, active, "a pid the OS reused must not keep blocking applies")
				return
			}
			require.Len(t, active, 1, "liveness plus an agreeing start time must report the writer")
			assert.Equal(t, 42, active[0].Pid)
			assert.Equal(t, witnessCwd, active[0].Cwd)
		})
	}
}

// TestFindActiveReportsSessionWithUnusableProcStart pins the sanctioned
// fallback: a procStart that is present but unreadable leaves the file judged by
// signal 0 alone, exactly as a file written before Claude Code recorded one.
func TestFindActiveReportsSessionWithUnusableProcStart(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]json.RawMessage
	}{
		{
			name:  "procStart is not a string",
			extra: map[string]json.RawMessage{procStartKey: json.RawMessage(`1932718445`)},
		},
		{
			name:  "procStart is not the ctime layout",
			extra: map[string]json.RawMessage{procStartKey: json.RawMessage(`"2031-01-02T03:04:05Z"`)},
		},
		{
			name:  "procStart is empty",
			extra: map[string]json.RawMessage{procStartKey: json.RawMessage(`""`)},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			home := newWitnessHome(t)
			writeWitnessSessionFile(t, home, "writer", SessionFile{Cwd: witnessCwd, Pid: 42, Extra: testCase.extra})
			workspace := NewWorkspaceForTest(home, noWitnessEnv, aliveOnly(42), noStartTimeProbe(t))

			active, err := workspace.ActiveWriters()

			require.NoError(t, err)
			require.Len(t, active, 1, "an unusable procStart leaves the file judged by liveness alone")
			assert.Equal(t, 42, active[0].Pid)
		})
	}
}

func newWitnessHome(t *testing.T) *Home {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "dotclaude")
	require.NoError(t, os.MkdirAll(directory, 0o750))
	return &Home{Dir: directory, ConfigFile: directory + ".json"}
}

// noWitnessEnv is the environment seam for tests whose code path never reads the
// environment.
func noWitnessEnv(string) string { return "" }

// aliveOnly reports exactly pid as alive.
func aliveOnly(pid int) func(int) bool {
	return func(candidate int) bool { return candidate == pid }
}

// noStartTimeProbe returns a start-time seam that fails the test when consulted.
// A session file with no usable procStart is judged by signal 0 alone, so no
// start time may be read for it.
func noStartTimeProbe(t *testing.T) func(int) (time.Time, error) {
	t.Helper()
	return func(int) (time.Time, error) {
		t.Fatal("start time read for a session file whose procStart is not usable")
		return time.Time{}, nil
	}
}

func writeWitnessSession(t *testing.T, home *Home, name string, pid int) {
	t.Helper()
	writeWitnessSessionFile(t, home, name, SessionFile{Cwd: witnessCwd, Pid: pid})
}

func writeWitnessSessionWithProcStart(t *testing.T, home *Home, name string, pid int, procStart string) {
	t.Helper()
	writeWitnessSessionFile(t, home, name, SessionFile{
		Cwd:   witnessCwd,
		Pid:   pid,
		Extra: map[string]json.RawMessage{procStartKey: json.RawMessage(strconv.Quote(procStart))},
	})
}

// fixtureHostProcStart reads the writer start time committed in the repo's
// session fixture, so the format these cases exercise is the one on disk rather
// than a second copy. It returns the raw `ps -o lstart=` string to write into a
// test session file, alongside the parsed value the cases offset from.
func fixtureHostProcStart(t *testing.T) (string, time.Time) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "dotclaude", "sessions", "24680.json"))
	require.NoError(t, err)

	var fixture SessionFile
	require.NoError(t, json.Unmarshal(data, &fixture))

	recorded, usable := parseProcStart(fixture)
	require.True(t, usable, "the committed session fixture must carry a usable procStart")

	var procStart string
	require.NoError(t, json.Unmarshal(fixture.Extra[procStartKey], &procStart))
	return procStart, recorded
}

func writeWitnessSessionFile(t *testing.T, home *Home, name string, sessionFile SessionFile) {
	t.Helper()
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	data, err := json.Marshal(sessionFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), name+".json"), data, 0o600))
}
