package remote_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/remote"
)

func TestSource_RejectsNilRemote(t *testing.T) {
	_, err := (&remote.Source{Remote: nil, Key: "k"}).Open(context.Background(), pipeline.Source{})
	if err == nil {
		t.Fatal("expected error on nil Remote")
	}
}

func TestSource_RejectsEmptyKey(t *testing.T) {
	r, _ := remote.New(context.Background(), memURL)
	defer func() { _ = r.Close() }()
	_, err := (&remote.Source{Remote: r, Key: ""}).Open(context.Background(), pipeline.Source{})
	if err == nil {
		t.Fatal("expected error on empty Key")
	}
}

func TestSource_OpenRoundTripMaterializesToTempfile(t *testing.T) {
	r, _ := remote.New(context.Background(), memURL)
	defer func() { _ = r.Close() }()

	want := []byte("source stage round trip")
	w, _ := r.Create(context.Background(), "k")
	_, _ = w.Write(want)
	_ = w.Close()

	src, err := (&remote.Source{Remote: r, Key: "k"}).Open(context.Background(), pipeline.Source{})
	if err != nil {
		t.Fatalf("Source.Open: %v", err)
	}
	defer func() { _ = src.Close() }()

	if src.Size != int64(len(want)) {
		t.Fatalf("Size = %d, want %d", src.Size, len(want))
	}
	got := make([]byte, src.Size)
	if _, err := src.ReaderAt.ReadAt(got, 0); err != nil && err != io.EOF {
		t.Fatalf("ReadAt: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSource_CloseRemovesTempfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tempdir listing semantics differ on Windows")
	}
	r, _ := remote.New(context.Background(), memURL)
	defer func() { _ = r.Close() }()
	w, _ := r.Create(context.Background(), "k")
	_, _ = w.Write([]byte("data"))
	_ = w.Close()

	src, err := (&remote.Source{Remote: r, Key: "k"}).Open(context.Background(), pipeline.Source{})
	if err != nil {
		t.Fatalf("Source.Open: %v", err)
	}

	tempfile, ok := src.ReaderAt.(*os.File)
	if !ok {
		t.Fatalf("ReaderAt = %T, want *os.File", src.ReaderAt)
	}
	tempPath := tempfile.Name()
	if !strings.HasPrefix(filepath.Base(tempPath), "cc-port-remote-") {
		t.Fatalf("tempPath = %q, want cc-port-remote-* prefix", tempPath)
	}

	if err := src.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("tempfile still exists after Close: %v", err)
	}
}

func TestSink_RejectsNilRemote(t *testing.T) {
	_, err := (&remote.Sink{Remote: nil, Key: "k"}).Open(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on nil Remote")
	}
}

func TestSink_RejectsEmptyKey(t *testing.T) {
	r, _ := remote.New(context.Background(), memURL)
	defer func() { _ = r.Close() }()
	_, err := (&remote.Sink{Remote: r, Key: ""}).Open(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on empty Key")
	}
}

func TestSink_OpenRoundTripCommitsOnClose(t *testing.T) {
	r, _ := remote.New(context.Background(), memURL)
	defer func() { _ = r.Close() }()

	w, err := (&remote.Sink{Remote: r, Key: "sinktest"}).Open(context.Background(), nil)
	if err != nil {
		t.Fatalf("Sink.Open: %v", err)
	}
	want := []byte("sink stage round trip")
	if _, err := w.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rc, err := r.Open(context.Background(), "sinktest")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
