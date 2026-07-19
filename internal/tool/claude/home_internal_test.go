package claude

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/lock"
)

func TestHomeAnchor_StripsTrailingSlash(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome+"/")

	resolved, err := homeAnchor(os.Getenv)

	require.NoError(t, err)
	assert.False(t, strings.HasSuffix(resolved, "/"), "anchor must not have trailing slash, got %q", resolved)
}

func TestHomeAnchor_RejectsRoot(t *testing.T) {
	t.Setenv("HOME", "/")

	_, err := homeAnchor(os.Getenv)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid home directory")
}

func TestHomeAnchor_RejectsEmpty(t *testing.T) {
	t.Setenv("HOME", "")

	_, err := homeAnchor(os.Getenv)

	require.Error(t, err)
}

func TestHomeAnchor_RejectsNonAbsolute(t *testing.T) {
	t.Setenv("HOME", "relative/home/path")

	_, err := homeAnchor(os.Getenv)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid home directory")
}

func TestHomeAnchor_FollowsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test exercises Linux/macOS behavior")
	}
	tempDir := t.TempDir()
	realHome := filepath.Join(tempDir, "real-home")
	require.NoError(t, os.MkdirAll(realHome, 0o750))
	linkHome := filepath.Join(tempDir, "link-home")
	require.NoError(t, os.Symlink(realHome, linkHome))
	t.Setenv("HOME", linkHome)

	resolved, err := homeAnchor(os.Getenv)

	require.NoError(t, err)
	expected, err := filepath.EvalSymlinks(realHome)
	require.NoError(t, err)
	assert.Equal(t, expected, resolved, "anchor must resolve through symlink to mirror projectPath treatment")
}

func TestScanHistoryLines_AcceptsLegalLongLine(t *testing.T) {
	line := strings.Repeat("x", MaxHistoryLine-1)

	lines, err := scanHistoryLines([]byte(line + "\n"))

	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Len(t, lines[0], len(line))
}

func TestWithLock_SucceedsWhenSessionPIDIsDead(t *testing.T) {
	home := &Home{Dir: t.TempDir()}
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(home.SessionsDir(), "dead-writer.json"),
		[]byte(`{"pid":4242,"cwd":"/test/project"}`),
		0o600,
	))
	workspace := NewWorkspaceForTest(home, os.Getenv, func(pid int) bool {
		assert.Equal(t, 4242, pid)
		return false
	})

	err := lock.WithLock(workspace.LockPath(), workspace.ActiveWriters, func() error { return nil })

	require.NoError(t, err)
}
