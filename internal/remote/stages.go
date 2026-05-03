package remote

import (
	"context"
	"fmt"
	"io"

	"github.com/it-bens/cc-port/internal/pipeline"
)

// Source is a pipeline.ReaderStage that opens the archive at Key on
// Remote and returns the gocloud reader as a streaming pipeline.View
// carrying the content length the bucket reported on open. Random-
// access materialization is the downstream MaterializeStage's
// responsibility; remote.Source neither stages tempfiles nor performs a
// separate stat (the Size travels in the View from gocloud's open
// response, with no extra round trip).
type Source struct {
	Remote *Remote
	Key    string
}

// Open returns View{Reader: rc, Size: rc.Size()} where rc is the bucket
// reader. The runner's close cascade owns rc.Close. remote.Source
// contributes no Meta.
func (s *Source) Open(ctx context.Context, _ pipeline.View) (pipeline.View, pipeline.Meta, io.Closer, error) {
	if s.Remote == nil {
		return pipeline.View{}, pipeline.Meta{}, nil, fmt.Errorf("remote.Source: Remote is nil")
	}
	if s.Key == "" {
		return pipeline.View{}, pipeline.Meta{}, nil, fmt.Errorf("remote.Source: Key is empty")
	}
	rc, err := s.Remote.Open(ctx, s.Key)
	if err != nil {
		return pipeline.View{}, pipeline.Meta{}, nil, err
	}
	return pipeline.View{Reader: rc, Size: rc.Size()}, pipeline.Meta{}, rc, nil
}

// Name identifies this stage in pipeline error messages.
func (s *Source) Name() string { return "remote source" }

// Sink is a pipeline.WriterStage that writes its bytes to Remote at
// Key. The returned writer is the bucket writer directly; closing it
// commits the upload.
type Sink struct {
	Remote *Remote
	Key    string
}

// Open returns the bucket writer for Key as both the writer and the
// closer. Closing the writer commits the upload; failure to close means
// no archive is visible on the remote.
func (s *Sink) Open(ctx context.Context, _ io.Writer) (io.Writer, io.Closer, error) {
	if s.Remote == nil {
		return nil, nil, fmt.Errorf("remote.Sink: Remote is nil")
	}
	if s.Key == "" {
		return nil, nil, fmt.Errorf("remote.Sink: Key is empty")
	}
	w, err := s.Remote.Create(ctx, s.Key)
	if err != nil {
		return nil, nil, err
	}
	return w, w, nil
}

// Name identifies this stage in pipeline error messages.
func (s *Sink) Name() string { return "remote sink" }
