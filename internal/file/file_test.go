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

	view, _, closer, err := (&file.Source{Path: path}).Open(context.Background(), pipeline.View{})
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = closer.Close() })

	require.NotNil(t, view.Reader, "Reader must be populated")
	require.NotNil(t, view.ReaderAt, "ReaderAt must be populated")
	assert.Equal(t, int64(len(want)), view.Size)

	got := make([]byte, len(want))
	_, err = view.ReaderAt.ReadAt(got, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt: %v", err)
	}
	assert.Equal(t, want, got)
}

func TestSource_OpenMissingWrapsError(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "missing.txt")

	_, _, _, err := (&file.Source{Path: path}).Open(context.Background(), pipeline.View{})

	require.Error(t, err, "expected error on missing file")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestSink_CreateNewFileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("0600 mode semantics differ on Windows")
	}
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "out.txt")

	w, closer, err := (&file.Sink{Path: path}).Open(context.Background(), nil)
	require.NoError(t, err, "Open")
	_, err = w.Write([]byte("data"))
	require.NoError(t, err, "Write")
	require.NoError(t, closer.Close(), "Close")

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

	w, closer, err := (&file.Sink{Path: path}).Open(context.Background(), nil)
	require.NoError(t, err, "Open")
	_, err = w.Write([]byte("new"))
	require.NoError(t, err, "Write")
	require.NoError(t, closer.Close(), "Close")

	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err, "ReadFile")
	assert.Equal(t, []byte("new"), got)
}
