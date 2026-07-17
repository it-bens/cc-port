package tool_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

func TestResolveProjectPath(t *testing.T) {
	tempDir := t.TempDir()

	// Resolve tempDir itself so /var -> /private/var symlinks on macOS do not
	// make the assertions below compare against the pre-symlink form.
	realTempDir, err := filepath.EvalSymlinks(tempDir)
	require.NoError(t, err)

	realProjectDir := filepath.Join(realTempDir, "real", "project")
	require.NoError(t, os.MkdirAll(realProjectDir, 0o750))

	linkDir := filepath.Join(realTempDir, "link")
	require.NoError(t, os.Symlink(filepath.Join(realTempDir, "real"), linkDir))

	t.Run("resolves symlink in existing path", func(t *testing.T) {
		resolved, err := tool.ResolveProjectPath(filepath.Join(linkDir, "project"))
		require.NoError(t, err)
		assert.Equal(t, realProjectDir, resolved)
	})

	t.Run("returns absolute form of relative path", func(t *testing.T) {
		workingDir, err := os.Getwd()
		require.NoError(t, err)
		realWorkingDir, err := filepath.EvalSymlinks(workingDir)
		require.NoError(t, err)

		resolved, err := tool.ResolveProjectPath("nonexistent-rel-path")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(realWorkingDir, "nonexistent-rel-path"), resolved)
	})
}

func TestResolveProjectPath_ExpandsLeadingTilde(t *testing.T) {
	fakeHome := t.TempDir()
	realFakeHome, err := filepath.EvalSymlinks(fakeHome)
	require.NoError(t, err)
	t.Setenv("HOME", fakeHome)

	target := filepath.Join(realFakeHome, "Projects", "myproject")
	require.NoError(t, os.MkdirAll(target, 0o750))

	resolved, err := tool.ResolveProjectPath("~/Projects/myproject")

	require.NoError(t, err)
	assert.Equal(t, target, resolved)
}

func TestResolveProjectPath_BareTildeIsLiteral(t *testing.T) {
	// ~user style is not expanded; the literal ~ survives to filepath.Abs.
	resolved, err := tool.ResolveProjectPath("~nonexistent-user/Projects")

	require.NoError(t, err)
	assert.Contains(t, resolved, "~nonexistent-user")
}
