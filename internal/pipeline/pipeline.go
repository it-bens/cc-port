// Package pipeline composes io.Writer chains (WriterStage) and View
// chains (ReaderStage) for cc-port's read and write data flows. Stages
// live in their owning packages; this package owns the interfaces, the
// runners, the close cascade, and the terminal MaterializeStage.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// View is the data carrier passed between reader stages. Reader is
// always set. ReaderAt and Size are populated by stages that can supply
// them (file source, MaterializeStage); transformers and streaming
// sources may leave them zero. Lifetime ownership is reported via a
// separate io.Closer return on stage Open so the runner assembles the
// close cascade.
type View struct {
	Reader   io.Reader
	ReaderAt io.ReaderAt
	Size     int64
}

// Meta carries dispatch observations contributed by stages during Open.
// Stages return Meta alongside View; the runner merges every stage's
// contribution into Source.Meta. Merge semantics: boolean fields OR
// across stages; non-boolean value-typed fields take the last non-zero
// contribution. Only one observation field exists today; future
// observations add a field with explicit merge semantics.
type Meta struct {
	WasEncrypted bool
}

// Source is what RunReader returns to the consumer. View carries the
// final stage's data shape. Meta carries the merged observations from
// every stage. Close walks every stage's closer in reverse chain order
// (latest stage first, source stage last), joins their errors with
// errors.Join, and is idempotent (second and later calls return nil).
type Source struct {
	View
	Meta  Meta
	Close func() error
}

// WriterStage is one step on the write path. Open returns the writer
// this stage exposes plus an optional closer for the stage's own
// resources. closer == nil means "passthrough; I own nothing." Sink
// stages receive nil downstream.
type WriterStage interface {
	Open(ctx context.Context, downstream io.Writer) (writer io.Writer, closer io.Closer, err error)
	Name() string
}

// ReaderStage is one step on the read path. Open returns the View this
// stage produces, the Meta it contributes (zero value when the stage
// observes nothing), and an optional closer for the stage's own
// resources. closer == nil means "passthrough; I own nothing." Source
// stages receive a zero View.
type ReaderStage interface {
	Open(ctx context.Context, upstream View) (data View, meta Meta, closer io.Closer, err error)
	Name() string
}

// RunWriter composes stages inside-out. The last stage opens with nil
// downstream (it is the sink). Each earlier stage opens with the
// previous stage's writer as its downstream. The runner accumulates
// every non-nil closer in chain order (outer-first, leaf-last). The
// returned io.WriteCloser forwards Write to the outermost stage's
// writer and Close walks the accumulated closers using errors.Join.
// Close is idempotent.
func RunWriter(ctx context.Context, stages []WriterStage) (io.WriteCloser, error) {
	if len(stages) == 0 {
		return nil, errors.New("pipeline: RunWriter requires at least one stage")
	}
	var writer io.Writer
	closers := make([]io.Closer, 0, len(stages))
	for i := len(stages) - 1; i >= 0; i-- {
		next, closer, err := stages[i].Open(ctx, writer)
		if err != nil {
			closeErr := walkClose(closers)
			return nil, errors.Join(
				fmt.Errorf("pipeline: open stage %q: %w", stages[i].Name(), err),
				closeErr,
			)
		}
		if closer != nil {
			closers = append([]io.Closer{closer}, closers...)
		}
		writer = next
	}
	if writer == nil {
		return nil, errors.New("pipeline: outermost stage returned nil writer")
	}
	return &chainWriteCloser{writer: writer, closers: closers}, nil
}

// RunReader composes stages outside-in. The first stage opens with the
// zero View (it is the source). Each later stage opens with the
// previous stage's View. The runner accumulates every non-nil closer in
// chain order (source-first, latest-last) and merges every stage's Meta
// contribution into a single Source.Meta as it walks. Returns a Source
// whose View is the final stage's output, whose Meta carries the merged
// observations, and whose Close walks the closers in reverse order
// (latest-first, source-last) using errors.Join. Close is idempotent.
func RunReader(ctx context.Context, stages []ReaderStage) (Source, error) {
	if len(stages) == 0 {
		return Source{}, errors.New("pipeline: RunReader requires at least one stage")
	}
	var current View
	var merged Meta
	closers := make([]io.Closer, 0, len(stages))
	for i, stage := range stages {
		next, contributed, closer, err := stage.Open(ctx, current)
		if err != nil {
			closeErr := walkCloseReverse(closers)
			return Source{}, errors.Join(
				fmt.Errorf("pipeline: open stage %q (position %d): %w", stage.Name(), i, err),
				closeErr,
			)
		}
		if closer != nil {
			closers = append(closers, closer)
		}
		mergeMeta(&merged, contributed)
		current = next
	}
	return Source{
		View:  current,
		Meta:  merged,
		Close: makeIdempotentReverseClose(closers),
	}, nil
}

// mergeMeta folds contributed into merged. Boolean fields OR across
// contributions. Non-boolean value-typed fields take the last non-zero
// contribution. Add a case here when introducing a new Meta field; the
// merge rule for that field MUST be explicit, never default-assigned.
func mergeMeta(merged *Meta, contributed Meta) {
	if contributed.WasEncrypted {
		merged.WasEncrypted = true
	}
}

// chainWriteCloser writes through the outermost stage and on Close
// walks every accumulated stage closer once, joining errors. Repeated
// Close calls return nil.
type chainWriteCloser struct {
	writer  io.Writer
	closers []io.Closer
	closed  bool
}

func (c *chainWriteCloser) Write(p []byte) (int, error) { return c.writer.Write(p) }

func (c *chainWriteCloser) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	return walkClose(c.closers)
}

// walkClose closes every io.Closer in the supplied order and joins
// non-nil errors with errors.Join.
func walkClose(closers []io.Closer) error {
	var errs []error
	for _, c := range closers {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// walkCloseReverse closes every io.Closer in reverse order and joins
// non-nil errors with errors.Join.
func walkCloseReverse(closers []io.Closer) error {
	var errs []error
	for i := len(closers) - 1; i >= 0; i-- {
		if err := closers[i].Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// makeIdempotentReverseClose returns a Close func that walks closers in
// reverse on first call, joins their errors, and returns nil on later
// calls.
func makeIdempotentReverseClose(closers []io.Closer) func() error {
	closed := false
	return func() error {
		if closed {
			return nil
		}
		closed = true
		return walkCloseReverse(closers)
	}
}
