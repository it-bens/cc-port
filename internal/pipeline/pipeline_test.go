package pipeline_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/it-bens/cc-port/internal/pipeline"
)

// trackedSink records writes and tracks Close ordering via the shared log.
type trackedSink struct {
	name string
	buf  *bytes.Buffer
	log  *[]string
}

func (s *trackedSink) Open(_ context.Context, _ io.Writer) (io.WriteCloser, error) {
	return &trackedSinkWriter{name: s.name, buf: s.buf, log: s.log}, nil
}
func (s *trackedSink) Name() string { return s.name }

type trackedSinkWriter struct {
	name string
	buf  *bytes.Buffer
	log  *[]string
}

func (w *trackedSinkWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (w *trackedSinkWriter) Close() error {
	*w.log = append(*w.log, "close:"+w.name)
	return nil
}

// trackedFilter wraps downstream and records its open/close sequence.
type trackedFilter struct {
	name string
	log  *[]string
}

func (f *trackedFilter) Open(_ context.Context, downstream io.Writer) (io.WriteCloser, error) {
	*f.log = append(*f.log, "open:"+f.name)
	return &trackedFilterWriter{name: f.name, downstream: downstream, log: f.log}, nil
}
func (f *trackedFilter) Name() string { return f.name }

type trackedFilterWriter struct {
	name       string
	downstream io.Writer
	log        *[]string
}

func (w *trackedFilterWriter) Write(p []byte) (int, error) { return w.downstream.Write(p) }
func (w *trackedFilterWriter) Close() error {
	*w.log = append(*w.log, "close:"+w.name)
	if dc, ok := w.downstream.(io.Closer); ok {
		return dc.Close()
	}
	return nil
}

type errorStage struct {
	name string
	err  error
}

func (s *errorStage) Open(_ context.Context, _ io.Writer) (io.WriteCloser, error) {
	return nil, s.err
}
func (s *errorStage) Name() string { return s.name }

func TestRunWriter_RejectsEmptyStages(t *testing.T) {
	if _, err := pipeline.RunWriter(context.Background(), nil); err == nil {
		t.Fatal("expected error on empty stage list")
	}
}

func TestRunWriter_SingleSinkRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	var log []string
	w, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&trackedSink{name: "sink", buf: &buf, log: &log},
	})
	if err != nil {
		t.Fatalf("RunWriter: %v", err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := buf.String(); got != "hello" {
		t.Fatalf("buf = %q, want %q", got, "hello")
	}
	if len(log) != 1 || log[0] != "close:sink" {
		t.Fatalf("log = %v, want [close:sink]", log)
	}
}

func TestRunWriter_FilterThenSinkClosesInOrder(t *testing.T) {
	var buf bytes.Buffer
	var log []string
	w, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&trackedFilter{name: "filter", log: &log},
		&trackedSink{name: "sink", buf: &buf, log: &log},
	})
	if err != nil {
		t.Fatalf("RunWriter: %v", err)
	}
	if _, err := w.Write([]byte("payload")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if buf.String() != "payload" {
		t.Fatalf("buf = %q, want %q", buf.String(), "payload")
	}
	wantLog := []string{"open:filter", "close:filter", "close:sink"}
	if len(log) != len(wantLog) {
		t.Fatalf("log = %v, want %v", log, wantLog)
	}
	for i := range wantLog {
		if log[i] != wantLog[i] {
			t.Fatalf("log[%d] = %q, want %q (full: %v)", i, log[i], wantLog[i], log)
		}
	}
}

func TestRunWriter_ErrorWrapsStageNameAndPosition(t *testing.T) {
	sentinel := errors.New("boom")
	_, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&errorStage{name: "broken", err: sentinel},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrap of sentinel", err)
	}
	if !contains(err.Error(), "broken") {
		t.Fatalf("err = %q, want substring %q", err.Error(), "broken")
	}
}

// --- Reader stage tests ---

type byteSource struct {
	name string
	data []byte
}

func (s *byteSource) Open(_ context.Context, _ pipeline.Source) (pipeline.Source, error) {
	return pipeline.Source{
		ReaderAt: bytes.NewReader(s.data),
		Size:     int64(len(s.data)),
		Close:    func() error { return nil },
	}, nil
}
func (s *byteSource) Name() string { return s.name }

// trackingReaderFilter records its close ordering and chains upstream.Close.
type trackingReaderFilter struct {
	name string
	log  *[]string
}

func (f *trackingReaderFilter) Open(_ context.Context, upstream pipeline.Source) (pipeline.Source, error) {
	*f.log = append(*f.log, "open:"+f.name)
	return pipeline.Source{
		ReaderAt: upstream.ReaderAt,
		Size:     upstream.Size,
		Close: func() error {
			*f.log = append(*f.log, "close:"+f.name)
			if upstream.Close != nil {
				return upstream.Close()
			}
			return nil
		},
	}, nil
}
func (f *trackingReaderFilter) Name() string { return f.name }

type errorReaderStage struct {
	name string
	err  error
}

func (s *errorReaderStage) Open(_ context.Context, _ pipeline.Source) (pipeline.Source, error) {
	return pipeline.Source{}, s.err
}
func (s *errorReaderStage) Name() string { return s.name }

func TestRunReader_RejectsEmptyStages(t *testing.T) {
	if _, err := pipeline.RunReader(context.Background(), nil); err == nil {
		t.Fatal("expected error on empty stage list")
	}
}

func TestRunReader_SourceRoundTrip(t *testing.T) {
	src, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		&byteSource{name: "byte", data: []byte("abcdef")},
	})
	if err != nil {
		t.Fatalf("RunReader: %v", err)
	}
	defer func() { _ = src.Close() }()
	if src.Size != 6 {
		t.Fatalf("Size = %d, want 6", src.Size)
	}
	buf := make([]byte, 6)
	n, err := src.ReaderAt.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt: %v", err)
	}
	if n != 6 || string(buf) != "abcdef" {
		t.Fatalf("ReadAt = %q (n=%d), want abcdef", string(buf[:n]), n)
	}
}

func TestRunReader_SourceThenFilterClosesInReverseChainOrder(t *testing.T) {
	var log []string
	src, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		&byteSource{name: "byte", data: []byte("x")},
		&trackingReaderFilter{name: "filter", log: &log},
	})
	if err != nil {
		t.Fatalf("RunReader: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wantLog := []string{"open:filter", "close:filter"}
	if len(log) != len(wantLog) {
		t.Fatalf("log = %v, want %v", log, wantLog)
	}
	for i := range wantLog {
		if log[i] != wantLog[i] {
			t.Fatalf("log[%d] = %q, want %q", i, log[i], wantLog[i])
		}
	}
}

func TestRunReader_ErrorWrapsStageNameAndPosition(t *testing.T) {
	sentinel := errors.New("nope")
	_, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		&byteSource{name: "byte", data: []byte("x")},
		&errorReaderStage{name: "broken", err: sentinel},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrap of sentinel", err)
	}
	if !contains(err.Error(), "broken") || !contains(err.Error(), "position 1") {
		t.Fatalf("err = %q, want substrings %q and %q", err.Error(), "broken", "position 1")
	}
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
