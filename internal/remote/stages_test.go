package remote_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/remote"
)

func TestSource_RejectsNilRemote(t *testing.T) {
	_, _, _, err := (&remote.Source{Remote: nil, Key: "k"}).Open(context.Background(), pipeline.View{})
	if err == nil {
		t.Fatal("expected error on nil Remote")
	}
}

func TestSource_RejectsEmptyKey(t *testing.T) {
	r, _ := remote.New(context.Background(), memURL)
	defer func() { _ = r.Close() }()
	_, _, _, err := (&remote.Source{Remote: r, Key: ""}).Open(context.Background(), pipeline.View{})
	if err == nil {
		t.Fatal("expected error on empty Key")
	}
}

func TestSource_OpenStreamsBucketReaderDirectly(t *testing.T) {
	r, _ := remote.New(context.Background(), memURL)
	defer func() { _ = r.Close() }()

	want := []byte("source stage round trip")
	w, _ := r.Create(context.Background(), "k")
	_, _ = w.Write(want)
	_ = w.Close()

	view, _, closer, err := (&remote.Source{Remote: r, Key: "k"}).Open(context.Background(), pipeline.View{})
	if err != nil {
		t.Fatalf("Source.Open: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if view.Reader == nil {
		t.Fatal("Reader must be populated for streaming source")
	}
	if view.ReaderAt != nil {
		t.Fatal("ReaderAt must be nil; remote source is streaming")
	}
	if view.Size != int64(len(want)) {
		t.Fatalf("Size = %d, want %d (gocloud-reported content length)", view.Size, len(want))
	}

	got, err := io.ReadAll(view.Reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSink_RejectsNilRemote(t *testing.T) {
	_, _, err := (&remote.Sink{Remote: nil, Key: "k"}).Open(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on nil Remote")
	}
}

func TestSink_RejectsEmptyKey(t *testing.T) {
	r, _ := remote.New(context.Background(), memURL)
	defer func() { _ = r.Close() }()
	_, _, err := (&remote.Sink{Remote: r, Key: ""}).Open(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on empty Key")
	}
}

func TestSink_OpenRoundTripCommitsOnClose(t *testing.T) {
	r, _ := remote.New(context.Background(), memURL)
	defer func() { _ = r.Close() }()

	w, closer, err := (&remote.Sink{Remote: r, Key: "sinktest"}).Open(context.Background(), nil)
	if err != nil {
		t.Fatalf("Sink.Open: %v", err)
	}
	want := []byte("sink stage round trip")
	if _, err := w.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := closer.Close(); err != nil {
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
