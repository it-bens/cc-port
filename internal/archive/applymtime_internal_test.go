package archive

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApplyMtime_NonZeroSetsBoth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600), "write file")

	expected := time.Date(2024, 8, 1, 12, 0, 0, 0, time.UTC)
	require.NoError(t, applyMtime(path, expected), "applyMtime")

	stat, err := os.Stat(path)
	require.NoError(t, err, "stat after applyMtime")
	require.WithinDuration(t, expected, stat.ModTime(), time.Second,
		"applyMtime should set mtime within FS resolution")
}

func TestApplyMtime_ZeroIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600), "write file")

	statBefore, err := os.Stat(path)
	require.NoError(t, err, "stat before")

	require.NoError(t, applyMtime(path, time.Time{}), "applyMtime with zero")

	statAfter, err := os.Stat(path)
	require.NoError(t, err, "stat after")
	require.Equal(t, statBefore.ModTime(), statAfter.ModTime(),
		"zero mtime should not modify the file")
}

func TestApplyMtime_NonExistentPathReturnsWrappedError(t *testing.T) {
	err := applyMtime("/nonexistent/path/that/does/not/exist", time.Now())
	require.Error(t, err, "applyMtime on missing path")
	require.Contains(t, err.Error(), "set mtime on",
		"error should be wrapped with 'set mtime on' prefix")
}
