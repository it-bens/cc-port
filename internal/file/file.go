// Package file provides pipeline source and sink stages that read from
// and write to local filesystem paths. Mode 0600 is enforced on Sink
// because cc-port archives are sensitive content regardless of
// encryption.
package file

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/it-bens/cc-port/internal/pipeline"
)

// Source is a pipeline ReaderStage that opens Path for reading.
type Source struct {
	Path string
}

// Open opens Path and returns it as a pipeline.View carrying the
// underlying *os.File as ReaderAt. The returned io.Closer is the same
// *os.File; the runner closes it.
func (s *Source) Open(_ context.Context, _ pipeline.View) (pipeline.View, io.Closer, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return pipeline.View{}, nil, fmt.Errorf("file source: open %s: %w", s.Path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return pipeline.View{}, nil, fmt.Errorf("file source: stat %s: %w", s.Path, err)
	}
	return pipeline.View{ReaderAt: f, Size: info.Size()}, f, nil
}

// Name returns the stage name used in pipeline error wrapping.
func (s *Source) Name() string { return "file source" }

// Sink is a pipeline WriterStage that creates Path for writing with
// mode 0600. Existing files are truncated.
type Sink struct {
	Path string
}

// Open creates or truncates Path with mode 0600 and returns it as both
// the stage's writer and its closer; the runner closes it.
func (s *Sink) Open(_ context.Context, _ io.Writer) (io.Writer, io.Closer, error) {
	f, err := os.OpenFile(s.Path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("file sink: open %s: %w", s.Path, err)
	}
	return f, f, nil
}

// Name returns the stage name used in pipeline error wrapping.
func (s *Sink) Name() string { return "file sink" }
