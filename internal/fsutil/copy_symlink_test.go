package fsutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyDir_PreservesSymlinkAsSymlink(t *testing.T) {
	source := t.TempDir()

	// Use a real file under t.TempDir() as the symlink target. If CopyDir
	// followed the symlink, the test would detect the smuggled bytes.
	outsideFile := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("SECRET-SHOULD-NOT-APPEAR"), 0o600))

	linkPath := filepath.Join(source, "escape")
	require.NoError(t, os.Symlink(outsideFile, linkPath))

	destination := filepath.Join(t.TempDir(), "dst")
	require.NoError(t, CopyDir(t.Context(), source, destination))

	info, err := os.Lstat(filepath.Join(destination, "escape"))
	require.NoError(t, err)
	assert.NotEqual(t, os.FileMode(0), info.Mode()&os.ModeSymlink, "destination entry must be a symlink")

	target, err := os.Readlink(filepath.Join(destination, "escape"))
	require.NoError(t, err)
	assert.Equal(t, outsideFile, target)
}

func TestCopyDir_HandlesSymlinkToDirectory(t *testing.T) {
	source := t.TempDir()
	realDir := filepath.Join(source, "real")
	require.NoError(t, os.Mkdir(realDir, 0o755)) //nolint:gosec // G301: test-controlled mode

	dirLink := filepath.Join(source, "via-link")
	require.NoError(t, os.Symlink(realDir, dirLink))

	destination := filepath.Join(t.TempDir(), "dst")
	require.NoError(t, CopyDir(t.Context(), source, destination))

	info, err := os.Lstat(filepath.Join(destination, "via-link"))
	require.NoError(t, err)
	assert.NotEqual(t, os.FileMode(0), info.Mode()&os.ModeSymlink)
}

func TestCopyDir_StreamsLargeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 64 MiB file test in short mode")
	}
	source := t.TempDir()
	src := filepath.Join(source, "big.bin")
	f, err := os.Create(src) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	chunk := make([]byte, 1<<20)
	for range 64 {
		_, err = f.Write(chunk)
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())

	destination := filepath.Join(t.TempDir(), "dst")
	require.NoError(t, CopyDir(t.Context(), source, destination))

	info, err := os.Stat(filepath.Join(destination, "big.bin"))
	require.NoError(t, err)
	assert.Equal(t, int64(64<<20), info.Size())
}
