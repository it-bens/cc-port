package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/it-bens/cc-port/internal/pipeline"
)

// Source is a pipeline.ReaderStage that reads the archive at Key from
// Remote, drains the bytes into a 0600 tempfile, and returns the
// tempfile as the new View. The runner removes the tempfile via the
// returned io.Closer.
type Source struct {
	Remote *Remote
	Key    string
}

// Open downloads the archive at Key from Remote, drains it into a 0600
// tempfile, and returns it as the View. The returned io.Closer closes
// the tempfile and removes it (joining errors via errors.Join). The
// runner owns idempotency.
func (s *Source) Open(ctx context.Context, _ pipeline.View) (pipeline.View, io.Closer, error) {
	if s.Remote == nil {
		return pipeline.View{}, nil, fmt.Errorf("remote.Source: Remote is nil")
	}
	if s.Key == "" {
		return pipeline.View{}, nil, fmt.Errorf("remote.Source: Key is empty")
	}
	rc, err := s.Remote.Open(ctx, s.Key)
	if err != nil {
		return pipeline.View{}, nil, err
	}
	defer func() { _ = rc.Close() }()
	return drainToTempfile(rc)
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

// drainToTempfile copies r into a 0600 tempfile and returns the
// tempfile as a View plus an io.Closer that closes the file and removes
// it. Close errors are joined via errors.Join; os.IsNotExist on Remove
// is filtered to nil.
func drainToTempfile(r io.Reader) (pipeline.View, io.Closer, error) {
	temp, err := os.CreateTemp("", "cc-port-remote-*.zip")
	if err != nil {
		return pipeline.View{}, nil, fmt.Errorf("remote: create tempfile: %w", err)
	}
	if err := os.Chmod(temp.Name(), 0o600); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return pipeline.View{}, nil, fmt.Errorf("remote: chmod tempfile: %w", err)
	}
	if _, err := io.Copy(temp, r); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return pipeline.View{}, nil, fmt.Errorf("remote: drain bytes: %w", err)
	}
	info, err := temp.Stat()
	if err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return pipeline.View{}, nil, fmt.Errorf("remote: stat tempfile: %w", err)
	}
	return pipeline.View{ReaderAt: temp, Size: info.Size()},
		&tempfileCloser{file: temp, path: temp.Name()},
		nil
}

// tempfileCloser closes the tempfile and removes it. errors.Join
// surfaces both failures; os.IsNotExist on Remove is treated as nil.
type tempfileCloser struct {
	file *os.File
	path string
}

func (c *tempfileCloser) Close() error {
	closeErr := c.file.Close()
	removeErr := os.Remove(c.path)
	if removeErr != nil && os.IsNotExist(removeErr) {
		removeErr = nil
	}
	return errors.Join(closeErr, removeErr)
}
