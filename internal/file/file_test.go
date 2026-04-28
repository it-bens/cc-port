package file_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/file"
	"github.com/it-bens/cc-port/internal/pipeline"
)

func TestSource_OpenExisting(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "in.txt")
	want := []byte("hello world")
	require.NoError(t, os.WriteFile(path, want, 0o600), "WriteFile")

	src, err := (&file.Source{Path: path}).Open(context.Background(), pipeline.Source{})
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = src.Close() })

	got := make([]byte, len(want))
	if _, err := src.ReaderAt.ReadAt(got, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt: %v", err)
	}

	assert.Equal(t, int64(len(want)), src.Size)
	assert.Equal(t, want, got)
}

func TestSource_OpenMissingWrapsError(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "missing.txt")

	_, err := (&file.Source{Path: path}).Open(context.Background(), pipeline.Source{})

	require.Error(t, err, "expected error on missing file")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestSink_CreateNewFileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("0600 mode semantics differ on Windows")
	}
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "out.txt")

	w, err := (&file.Sink{Path: path}).Open(context.Background(), nil)
	require.NoError(t, err, "Open")
	_, err = w.Write([]byte("data"))
	require.NoError(t, err, "Write")
	require.NoError(t, w.Close(), "Close")

	info, err := os.Stat(path)
	require.NoError(t, err, "Stat")
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err, "ReadFile")
	assert.Equal(t, []byte("data"), got)
}

func TestSink_OverwritesExistingFile(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "out.txt")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600), "WriteFile")

	w, err := (&file.Sink{Path: path}).Open(context.Background(), nil)
	require.NoError(t, err, "Open")
	_, err = w.Write([]byte("new"))
	require.NoError(t, err, "Write")
	require.NoError(t, w.Close(), "Close")

	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err, "ReadFile")
	assert.Equal(t, []byte("new"), got)
}
