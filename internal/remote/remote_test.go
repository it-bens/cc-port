package remote_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"testing"

	_ "gocloud.dev/blob/memblob"

	"github.com/it-bens/cc-port/internal/remote"
)

const memURL = "mem://"

func TestNew_GarbageURLReturnsError(t *testing.T) {
	if _, err := remote.New(context.Background(), "::not-a-url::"); err == nil {
		t.Fatal("expected error on malformed URL")
	}
}

func TestRemote_OpenMissingReturnsErrNotFound(t *testing.T) {
	r, err := remote.New(context.Background(), memURL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()
	_, err = r.Open(context.Background(), "missing-key")
	if !errors.Is(err, remote.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRemote_StatMissingReturnsErrNotFound(t *testing.T) {
	r, err := remote.New(context.Background(), memURL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()
	_, err = r.Stat(context.Background(), "missing-key")
	if !errors.Is(err, remote.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRemote_CreateThenOpenRoundTrip(t *testing.T) {
	r, err := remote.New(context.Background(), memURL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	want := []byte("payload bytes for round trip")
	w, err := r.Create(context.Background(), "myproject")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := w.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rc, err := r.Open(context.Background(), "myproject")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRemote_StatAfterCreateReturnsSize(t *testing.T) {
	r, err := remote.New(context.Background(), memURL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	body := []byte("five-five-five-fivefive!")
	w, _ := r.Create(context.Background(), "k")
	_, _ = w.Write(body)
	_ = w.Close()

	attrs, err := r.Stat(context.Background(), "k")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if attrs.Size != int64(len(body)) {
		t.Fatalf("Size = %d, want %d", attrs.Size, len(body))
	}
	if attrs.ModTime.IsZero() {
		t.Fatal("ModTime zero, want non-zero")
	}
}

func TestRemote_FileBackendRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file URL formatting differs on Windows; covered by integration tests")
	}
	tempDir := t.TempDir()
	url := "file://" + filepath.ToSlash(tempDir)
	r, err := remote.New(context.Background(), url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	want := []byte("file-backend round trip")
	w, _ := r.Create(context.Background(), "filetest")
	_, _ = w.Write(want)
	_ = w.Close()

	rc, err := r.Open(context.Background(), "filetest")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
