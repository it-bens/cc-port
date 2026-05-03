package pipeline_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/pipeline"
)

// trackedSink is a leaf WriterStage that buffers bytes and records its
// Close in the shared log.
type trackedSink struct {
	name string
	buf  *bytes.Buffer
	log  *[]string
}

func (s *trackedSink) Open(_ context.Context, _ io.Writer) (io.Writer, io.Closer, error) {
	return &trackedSinkWriter{buf: s.buf}, &trackedSinkCloser{name: s.name, log: s.log}, nil
}
func (s *trackedSink) Name() string { return s.name }

type trackedSinkWriter struct{ buf *bytes.Buffer }

func (w *trackedSinkWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

type trackedSinkCloser struct {
	name string
	log  *[]string
}

func (c *trackedSinkCloser) Close() error {
	*c.log = append(*c.log, "close:"+c.name)
	return nil
}

// trackedFilter is a filter WriterStage that records open and close in
// the shared log. Its writer forwards to downstream; the runner cascades
// close — the filter does not.
type trackedFilter struct {
	name string
	log  *[]string
}

func (f *trackedFilter) Open(_ context.Context, downstream io.Writer) (io.Writer, io.Closer, error) {
	*f.log = append(*f.log, "open:"+f.name)
	return downstream, &trackedFilterCloser{name: f.name, log: f.log}, nil
}
func (f *trackedFilter) Name() string { return f.name }

type trackedFilterCloser struct {
	name string
	log  *[]string
}

func (c *trackedFilterCloser) Close() error {
	*c.log = append(*c.log, "close:"+c.name)
	return nil
}

// passthroughFilter returns the downstream writer unchanged and reports
// nil closer. The runner must skip nil closers in the cascade.
type passthroughFilter struct{ name string }

func (f *passthroughFilter) Open(_ context.Context, downstream io.Writer) (io.Writer, io.Closer, error) {
	return downstream, nil, nil
}
func (f *passthroughFilter) Name() string { return f.name }

type errorWriterStage struct {
	name string
	err  error
}

func (s *errorWriterStage) Open(_ context.Context, _ io.Writer) (io.Writer, io.Closer, error) {
	return nil, nil, s.err
}
func (s *errorWriterStage) Name() string { return s.name }

type errClosingCloser struct {
	err   error
	count *int
}

func (c *errClosingCloser) Close() error { *c.count++; return c.err }

func TestRunWriter_RejectsEmptyStages(t *testing.T) {
	_, err := pipeline.RunWriter(context.Background(), nil)
	require.Error(t, err)
}

func TestRunWriter_SingleSinkRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	var log []string

	w, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&trackedSink{name: "sink", buf: &buf, log: &log},
	})
	require.NoError(t, err)
	_, err = w.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	assert.Equal(t, "hello", buf.String())
	assert.Equal(t, []string{"close:sink"}, log)
}

func TestRunWriter_FilterThenSinkClosesInChainOrder(t *testing.T) {
	var buf bytes.Buffer
	var log []string

	w, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&trackedFilter{name: "filter", log: &log},
		&trackedSink{name: "sink", buf: &buf, log: &log},
	})
	require.NoError(t, err)
	_, err = w.Write([]byte("payload"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	assert.Equal(t, "payload", buf.String())
	assert.Equal(t, []string{"open:filter", "close:filter", "close:sink"}, log)
}

func TestRunWriter_PassthroughStageContributesNoCloser(t *testing.T) {
	var buf bytes.Buffer
	var log []string

	w, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&passthroughFilter{name: "passthrough"},
		&trackedSink{name: "sink", buf: &buf, log: &log},
	})
	require.NoError(t, err)
	require.NoError(t, w.Close())

	assert.Equal(t, []string{"close:sink"}, log,
		"passthrough must not add a close entry to the chain")
}

func TestRunWriter_OuterCloseIsIdempotent(t *testing.T) {
	calls := 0
	stage := &countingCloseSink{count: &calls}

	w, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{stage})
	require.NoError(t, err)
	require.NoError(t, w.Close())
	require.NoError(t, w.Close(), "second Close must return nil")

	assert.Equal(t, 1, calls, "stage close must run exactly once")
}

func TestRunWriter_OuterCloseJoinsStageErrors(t *testing.T) {
	a := errors.New("close-a-failed")
	b := errors.New("close-b-failed")
	countA, countB := 0, 0

	w, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&errClosingFilter{name: "a", err: a, count: &countA},
		&errClosingSink{err: b, count: &countB},
	})
	require.NoError(t, err)

	closeErr := w.Close()
	require.Error(t, closeErr)
	require.ErrorIs(t, closeErr, a)
	require.ErrorIs(t, closeErr, b)
	assert.Equal(t, 1, countA)
	assert.Equal(t, 1, countB)
}

func TestRunWriter_OpenErrorClosesAccumulated(t *testing.T) {
	calls := 0
	sentinel := errors.New("open-broken")

	_, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&errorWriterStage{name: "broken", err: sentinel},
		&countingCloseSink{count: &calls},
	})
	require.ErrorIs(t, err, sentinel)
	require.ErrorContains(t, err, "broken")
	assert.Equal(t, 1, calls,
		"already-opened stage closer must run when a later stage fails Open")
}

// --- Reader stage helpers and tests ---

type byteSource struct {
	name string
	data []byte
	log  *[]string
}

func (s *byteSource) Open(_ context.Context, _ pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	if s.log != nil {
		*s.log = append(*s.log, "open:"+s.name)
	}
	reader := bytes.NewReader(s.data)
	return pipeline.View{
			Reader:   reader,
			ReaderAt: reader,
			Size:     int64(len(s.data)),
		},
		pipeline.Meta{},
		&loggingCloser{name: s.name, log: s.log},
		nil
}
func (s *byteSource) Name() string { return s.name }

// trackingReaderFilter records open and close in the shared log. It
// reports the upstream View unchanged (a pass-through filter that still
// owns a side resource the runner must close).
type trackingReaderFilter struct {
	name string
	log  *[]string
}

func (f *trackingReaderFilter) Open(_ context.Context, upstream pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	*f.log = append(*f.log, "open:"+f.name)
	return upstream, pipeline.Meta{}, &loggingCloser{name: f.name, log: f.log}, nil
}
func (f *trackingReaderFilter) Name() string { return f.name }

type passthroughReaderFilter struct{ name string }

func (f *passthroughReaderFilter) Open(_ context.Context, upstream pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	return upstream, pipeline.Meta{}, nil, nil
}
func (f *passthroughReaderFilter) Name() string { return f.name }

type errorReaderStage struct {
	name string
	err  error
}

func (s *errorReaderStage) Open(_ context.Context, _ pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	return pipeline.View{}, pipeline.Meta{}, nil, s.err
}
func (s *errorReaderStage) Name() string { return s.name }

type loggingCloser struct {
	name string
	log  *[]string
}

func (c *loggingCloser) Close() error {
	if c.log != nil {
		*c.log = append(*c.log, "close:"+c.name)
	}
	return nil
}

func TestRunReader_RejectsEmptyStages(t *testing.T) {
	_, err := pipeline.RunReader(context.Background(), nil)
	require.Error(t, err)
}

func TestRunReader_SourceRoundTrip(t *testing.T) {
	src, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		&byteSource{name: "byte", data: []byte("abcdef")},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = src.Close() })

	buf := make([]byte, 6)
	n, err := src.ReaderAt.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt: %v", err)
	}
	assert.Equal(t, int64(6), src.Size)
	assert.Equal(t, 6, n)
	assert.Equal(t, "abcdef", string(buf))
}

func TestRunReader_SourceThenFilterClosesInReverseChainOrder(t *testing.T) {
	var log []string

	src, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		&byteSource{name: "byte", data: []byte("x"), log: &log},
		&trackingReaderFilter{name: "filter", log: &log},
	})
	require.NoError(t, err)
	require.NoError(t, src.Close())

	assert.Equal(t,
		[]string{"open:byte", "open:filter", "close:filter", "close:byte"},
		log,
		"filter must close before source")
}

func TestRunReader_PassthroughStageContributesNoCloser(t *testing.T) {
	var log []string

	src, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		&byteSource{name: "byte", data: []byte("x"), log: &log},
		&passthroughReaderFilter{name: "passthrough"},
	})
	require.NoError(t, err)
	require.NoError(t, src.Close())

	assert.Equal(t, []string{"open:byte", "close:byte"}, log,
		"passthrough must not add a close entry to the chain")
}

func TestRunReader_OuterCloseIsIdempotent(t *testing.T) {
	calls := 0

	src, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		&countingCloseSource{count: &calls},
	})
	require.NoError(t, err)
	require.NoError(t, src.Close())
	require.NoError(t, src.Close(), "second Close must return nil")

	assert.Equal(t, 1, calls, "stage close must run exactly once")
}

func TestRunReader_OuterCloseJoinsStageErrors(t *testing.T) {
	a := errors.New("source-close-failed")
	b := errors.New("filter-close-failed")
	countA, countB := 0, 0

	src, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		&errClosingSource{err: a, count: &countA},
		&errClosingReaderFilter{err: b, count: &countB},
	})
	require.NoError(t, err)

	closeErr := src.Close()
	require.Error(t, closeErr)
	require.ErrorIs(t, closeErr, a)
	require.ErrorIs(t, closeErr, b)
	assert.Equal(t, 1, countA)
	assert.Equal(t, 1, countB)
}

func TestRunReader_OpenErrorClosesAccumulated(t *testing.T) {
	calls := 0
	sentinel := errors.New("nope")

	_, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		&countingCloseSource{count: &calls},
		&errorReaderStage{name: "broken", err: sentinel},
	})
	require.ErrorIs(t, err, sentinel)
	require.ErrorContains(t, err, "broken")
	require.ErrorContains(t, err, "position 1")
	assert.Equal(t, 1, calls,
		"already-opened source's closer must run when a later stage fails Open")
}

// --- Helper stages used only by close-error and counting tests ---

type countingCloseSink struct{ count *int }

func (s *countingCloseSink) Open(_ context.Context, _ io.Writer) (io.Writer, io.Closer, error) {
	return io.Discard, &errClosingCloser{count: s.count}, nil
}
func (s *countingCloseSink) Name() string { return "counting-sink" }

type countingCloseSource struct{ count *int }

func (s *countingCloseSource) Open(_ context.Context, _ pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	reader := bytes.NewReader(nil)
	return pipeline.View{Reader: reader, ReaderAt: reader, Size: 0},
		pipeline.Meta{},
		&errClosingCloser{count: s.count},
		nil
}
func (s *countingCloseSource) Name() string { return "counting-source" }

type errClosingFilter struct {
	name  string
	err   error
	count *int
}

func (f *errClosingFilter) Open(_ context.Context, downstream io.Writer) (io.Writer, io.Closer, error) {
	return downstream, &errClosingCloser{err: f.err, count: f.count}, nil
}
func (f *errClosingFilter) Name() string { return f.name }

type errClosingSink struct {
	err   error
	count *int
}

func (s *errClosingSink) Open(_ context.Context, _ io.Writer) (io.Writer, io.Closer, error) {
	return io.Discard, &errClosingCloser{err: s.err, count: s.count}, nil
}
func (s *errClosingSink) Name() string { return "err-closing-sink" }

type errClosingSource struct {
	err   error
	count *int
}

func (s *errClosingSource) Open(_ context.Context, _ pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	reader := bytes.NewReader(nil)
	return pipeline.View{Reader: reader, ReaderAt: reader},
		pipeline.Meta{},
		&errClosingCloser{err: s.err, count: s.count},
		nil
}
func (s *errClosingSource) Name() string { return "err-closing-source" }

type errClosingReaderFilter struct {
	err   error
	count *int
}

func (f *errClosingReaderFilter) Open(_ context.Context, upstream pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	return upstream, pipeline.Meta{}, &errClosingCloser{err: f.err, count: f.count}, nil
}
func (f *errClosingReaderFilter) Name() string { return "err-closing-filter" }

// metaContributingStage is a test ReaderStage that returns the supplied
// Meta and forwards upstream's View unchanged. Used to drive RunReader's
// Meta merge.
type metaContributingStage struct {
	name string
	meta pipeline.Meta
}

func (s *metaContributingStage) Open(_ context.Context, upstream pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	return upstream, s.meta, nil, nil
}
func (s *metaContributingStage) Name() string { return s.name }

func TestRunReader_MergesMetaAcrossStages(t *testing.T) {
	src := &byteSource{
		name: "source",
		data: []byte("payload"),
		log:  &[]string{},
	}

	source, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		src,
		&metaContributingStage{name: "observer", meta: pipeline.Meta{WasEncrypted: true}},
	})

	require.NoError(t, err)
	t.Cleanup(func() { _ = source.Close() })
	assert.True(t, source.Meta.WasEncrypted, "WasEncrypted contribution must surface in Source.Meta")
}

func TestRunReader_LaterStageDoesNotClearEarlierMeta(t *testing.T) {
	src := &byteSource{
		name: "source",
		data: []byte("payload"),
		log:  &[]string{},
	}

	source, err := pipeline.RunReader(context.Background(), []pipeline.ReaderStage{
		src,
		&metaContributingStage{name: "observer", meta: pipeline.Meta{WasEncrypted: true}},
		&metaContributingStage{name: "passthrough", meta: pipeline.Meta{}},
	})

	require.NoError(t, err)
	t.Cleanup(func() { _ = source.Close() })
	assert.True(t, source.Meta.WasEncrypted, "later stage's zero Meta must not clear an earlier true contribution")
}
