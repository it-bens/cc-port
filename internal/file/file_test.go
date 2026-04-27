package file_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/it-bens/cc-port/internal/file"
	"github.com/it-bens/cc-port/internal/pipeline"
)

func TestSource_OpenExisting(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "in.txt")
	want := []byte("hello world")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	src, err := (&file.Source{Path: path}).Open(context.Background(), pipeline.Source{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = src.Close() }()
	if src.Size != int64(len(want)) {
		t.Fatalf("Size = %d, want %d", src.Size, len(want))
	}
	got := make([]byte, len(want))
	if _, err := src.ReaderAt.ReadAt(got, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadAt = %q, want %q", string(got), string(want))
	}
}

func TestSource_OpenMissingWrapsError(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "missing.txt")
	_, err := (&file.Source{Path: path}).Open(context.Background(), pipeline.Source{})
	if err == nil {
		t.Fatal("expected error on missing file")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected not-exist error, got %v", err)
	}
}

func TestSink_CreateNewFileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("0600 mode semantics differ on Windows")
	}
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "out.txt")
	w, err := (&file.Sink{Path: path}).Open(context.Background(), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := w.Write([]byte("data")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0o600", info.Mode().Perm())
	}
	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "data" {
		t.Fatalf("contents = %q, want data", string(got))
	}
}

func TestSink_OverwritesExistingFile(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	w, err := (&file.Sink{Path: path}).Open(context.Background(), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := w.Write([]byte("new")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("contents = %q, want new", string(got))
	}
}
