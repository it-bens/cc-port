package main

import (
	"context"
	"errors"
	"io"

	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/progress"
)

// downloadCounterStage is a pipeline.ReaderStage that opens the "download"
// byte phase against the remote blob size and counts the streamed transfer
// as the drain pulls it through. Inserted between remote.Source and the
// decrypt stage so it measures the encrypted bytes coming off the remote.
type downloadCounterStage struct {
	reporter progress.Reporter
	handle   progress.PhaseHandle
}

func (stage *downloadCounterStage) Open(_ context.Context, upstream pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	if upstream.Reader == nil {
		return pipeline.View{}, pipeline.Meta{}, nil, errors.New("downloadCounterStage: upstream Reader is nil")
	}
	stage.handle = stage.reporter.Phase("download", upstream.Size, progress.UnitBytes)
	return pipeline.View{
		Reader: progress.CountingReader(upstream.Reader, stage.handle),
		Size:   upstream.Size,
	}, pipeline.Meta{}, nil, nil
}

func (stage *downloadCounterStage) Name() string { return "download counter" }

// End closes the download phase. Call only after a successful drain.
func (stage *downloadCounterStage) End() {
	if stage.handle != nil {
		stage.handle.End("")
	}
}
