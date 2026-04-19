package fsutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/fsutil"
)

//nolint:funlen // subtests share tempdir + symlink scaffolding; splitting would duplicate the fixture.
func TestResolveExistingAncestor(t *testing.T) {
	tempDir := t.TempDir()

	// Resolve tempDir itself so /var -> /private/var symlinks on macOS do
	// not make the assertions below compare against the pre-symlink form.
	realTempDir, err := filepath.EvalSymlinks(tempDir)
	require.NoError(t, err)

	realProjectDir := filepath.Join(realTempDir, "real", "project")
	require.NoError(t, os.MkdirAll(realProjectDir, 0o750))

	linkDir := filepath.Join(realTempDir, "link")
	require.NoError(t, os.Symlink(filepath.Join(realTempDir, "real"), linkDir))

	t.Run("resolves symlink in existing path", func(t *testing.T) {
		resolved, err := fsutil.ResolveExistingAncestor(filepath.Join(linkDir, "project"))
		require.NoError(t, err)
		assert.Equal(t, realProjectDir, resolved)
	})

	t.Run("preserves single missing trailing component", func(t *testing.T) {
		resolved, err := fsutil.ResolveExistingAncestor(filepath.Join(linkDir, "new-project"))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(realTempDir, "real", "new-project"), resolved)
	})

	t.Run("preserves multiple missing trailing components", func(t *testing.T) {
		resolved, err := fsutil.ResolveExistingAncestor(filepath.Join(linkDir, "a", "b", "c"))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(realTempDir, "real", "a", "b", "c"), resolved)
	})

	t.Run("root passes through unchanged", func(t *testing.T) {
		resolved, err := fsutil.ResolveExistingAncestor("/")
		require.NoError(t, err)
		assert.Equal(t, "/", resolved)
	})

	t.Run("fully existing path without symlinks is unchanged", func(t *testing.T) {
		resolved, err := fsutil.ResolveExistingAncestor(realProjectDir)
		require.NoError(t, err)
		assert.Equal(t, realProjectDir, resolved)
	})

	t.Run("broken symlink surfaces EvalSymlinks error", func(t *testing.T) {
		brokenLink := filepath.Join(realTempDir, "broken")
		require.NoError(t, os.Symlink(filepath.Join(realTempDir, "does-not-exist-target"), brokenLink))

		_, err := fsutil.ResolveExistingAncestor(brokenLink)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "resolve symlinks for")
	})

	t.Run("non-ENOENT stat error surfaces", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("permission-based stat error is not reproducible as root")
		}

		restrictedDir := filepath.Join(realTempDir, "restricted")
		require.NoError(t, os.Mkdir(restrictedDir, 0o000))
		t.Cleanup(func() {
			// Restore permissions so t.TempDir cleanup can remove the tree.
			_ = os.Chmod(restrictedDir, 0o750) //nolint:gosec // G302: restore perms for t.TempDir cleanup
		})

		_, err := fsutil.ResolveExistingAncestor(filepath.Join(restrictedDir, "child"))
		require.Error(t, err)
		// The error is either a "stat" error from the walk-up loop or an
		// "resolve symlinks" error from EvalSymlinks on the restricted dir.
		// Both are acceptable contract-honouring failures; only ENOENT
		// would be a regression.
		assert.NotErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("panics on relative input", func(t *testing.T) {
		require.PanicsWithValue(
			t,
			`fsutil.ResolveExistingAncestor: path must be absolute, got "relative/path"`,
			func() { _, _ = fsutil.ResolveExistingAncestor("relative/path") },
		)
	})

	t.Run("panics on empty input", func(t *testing.T) {
		require.PanicsWithValue(
			t,
			`fsutil.ResolveExistingAncestor: path must be absolute, got ""`,
			func() { _, _ = fsutil.ResolveExistingAncestor("") },
		)
	})
}
