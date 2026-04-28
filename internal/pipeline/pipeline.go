// Package pipeline composes io.Writer chains (WriterStage) and Source chains
// (ReaderStage) for cc-port's read and write data flows. Stages live in their
// owning packages; this package owns the interfaces and the runners.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// Source carries the result of a read-side stage: random-access bytes,
// their size, and the cleanup function that releases this stage's
// resources. Filter stages chain their Close to upstream's Close so the
// consumer calls Source.Close once and gets correct cleanup of every
// stage in the chain.
type Source struct {
	ReaderAt io.ReaderAt
	Size     int64
	Close    func() error
}

// WriterStage is one step on the write path. Open returns the writer this
// stage exposes; bytes written there pass through this stage and forward
// to downstream. Sink stages ignore downstream because they ARE the sink.
type WriterStage interface {
	Open(ctx context.Context, downstream io.Writer) (io.WriteCloser, error)
	Name() string
}

// ReaderStage is one step on the read path. Open consumes upstream and
// returns this stage's Source. Source stages ignore upstream because they
// ARE the source.
type ReaderStage interface {
	Open(ctx context.Context, upstream Source) (Source, error)
	Name() string
}

// RunWriter composes stages inside-out. The last stage opens with nil
// downstream (it is the sink) and returns the actual writer. Each earlier
// stage opens with the previous stage's writer as its downstream. Returns
// the outermost writer: what the producer writes plaintext into. Closing
// the returned writer flushes each stage in chain order.
func RunWriter(ctx context.Context, stages []WriterStage) (io.WriteCloser, error) {
	if len(stages) == 0 {
		return nil, errors.New("pipeline: RunWriter requires at least one stage")
	}
	var current io.WriteCloser
	for i := len(stages) - 1; i >= 0; i-- {
		var downstream io.Writer
		if current != nil {
			downstream = current
		}
		next, err := stages[i].Open(ctx, downstream)
		if err != nil {
			if current != nil {
				_ = current.Close()
			}
			return nil, fmt.Errorf("pipeline: open stage %q: %w", stages[i].Name(), err)
		}
		current = next
	}
	return current, nil
}

// RunReader composes stages outside-in. The first stage opens with the zero
// Source (it is the source) and returns its produced Source. Each later
// stage opens with the previous stage's Source as upstream. Returns the
// final Source. Each stage's Source.Close cleans its own resources and
// then calls upstream.Close, so the consumer calls one Close on the
// returned Source and the entire chain unwinds.
func RunReader(ctx context.Context, stages []ReaderStage) (Source, error) {
	if len(stages) == 0 {
		return Source{}, errors.New("pipeline: RunReader requires at least one stage")
	}
	var current Source
	for i, stage := range stages {
		next, err := stage.Open(ctx, current)
		if err != nil {
			if current.Close != nil {
				_ = current.Close()
			}
			return Source{}, fmt.Errorf("pipeline: open stage %q (position %d): %w", stage.Name(), i, err)
		}
		current = next
	}
	return current, nil
}
