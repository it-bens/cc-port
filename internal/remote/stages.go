package remote

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/it-bens/cc-port/internal/pipeline"
)

// Source is a pipeline.ReaderStage that reads the archive at Key from
// Remote, drains the bytes into a 0600 tempfile, and returns the
// tempfile as the new Source's ReaderAt. Source.Close removes the
// tempfile.
type Source struct {
	Remote *Remote
	Key    string
}

// Open downloads the archive at Key from Remote, drains it into a 0600
// tempfile, and returns the tempfile as the new pipeline.Source. The
// returned Source.Close removes the tempfile.
func (s *Source) Open(ctx context.Context, _ pipeline.Source) (pipeline.Source, error) {
	if s.Remote == nil {
		return pipeline.Source{}, fmt.Errorf("remote.Source: Remote is nil")
	}
	if s.Key == "" {
		return pipeline.Source{}, fmt.Errorf("remote.Source: Key is empty")
	}
	rc, err := s.Remote.Open(ctx, s.Key)
	if err != nil {
		return pipeline.Source{}, err
	}
	defer func() { _ = rc.Close() }()
	return drainToTempfile(rc)
}

// Name identifies this stage in pipeline error messages.
func (s *Source) Name() string { return "remote source" }

// Sink is a pipeline.WriterStage that writes its bytes to Remote at
// Key. The returned WriteCloser is the bucket writer directly; closing
// commits the upload.
type Sink struct {
	Remote *Remote
	Key    string
}

// Open returns the bucket writer for Key. Closing the returned
// WriteCloser commits the upload; failure to close means no archive is
// visible on the remote.
func (s *Sink) Open(ctx context.Context, _ io.Writer) (io.WriteCloser, error) {
	if s.Remote == nil {
		return nil, fmt.Errorf("remote.Sink: Remote is nil")
	}
	if s.Key == "" {
		return nil, fmt.Errorf("remote.Sink: Key is empty")
	}
	return s.Remote.Create(ctx, s.Key)
}

// Name identifies this stage in pipeline error messages.
func (s *Sink) Name() string { return "remote sink" }

// drainToTempfile copies r into a 0600 tempfile and returns a
// pipeline.Source whose ReaderAt is the tempfile, Size is from Stat,
// and Close removes the tempfile (idempotent).
func drainToTempfile(r io.Reader) (pipeline.Source, error) {
	temp, err := os.CreateTemp("", "cc-port-remote-*.zip")
	if err != nil {
		return pipeline.Source{}, fmt.Errorf("remote: create tempfile: %w", err)
	}
	if err := os.Chmod(temp.Name(), 0o600); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return pipeline.Source{}, fmt.Errorf("remote: chmod tempfile: %w", err)
	}
	if _, err := io.Copy(temp, r); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return pipeline.Source{}, fmt.Errorf("remote: drain bytes: %w", err)
	}
	info, err := temp.Stat()
	if err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return pipeline.Source{}, fmt.Errorf("remote: stat tempfile: %w", err)
	}
	tempPath := temp.Name()
	closed := false
	return pipeline.Source{
		ReaderAt: temp,
		Size:     info.Size(),
		Close: func() error {
			if closed {
				return nil
			}
			closed = true
			closeErr := temp.Close()
			removeErr := os.Remove(tempPath)
			switch {
			case closeErr != nil:
				return closeErr
			case removeErr != nil && !os.IsNotExist(removeErr):
				return removeErr
			default:
				return nil
			}
		},
	}, nil
}
