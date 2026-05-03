package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// MaterializeStage is the terminal ReaderStage in any chain whose
// downstream consumer needs random access (io.ReaderAt). Open
// short-circuits when upstream.ReaderAt is non-nil; otherwise it drains
// upstream.Reader to a 0600 tempfile and returns the tempfile as the
// new View. The tempfile suffix is deliberately format-agnostic so
// future archive formats need no change here.
type MaterializeStage struct{}

// Open returns upstream unchanged when upstream.ReaderAt is non-nil
// (zero closer; the runner already manages the upstream's closer). When
// upstream.ReaderAt is nil, drains upstream.Reader to a 0600 tempfile
// and returns View{Reader: temp, ReaderAt: temp, Size: stat.Size()}
// with a closer that closes the file and removes it.
func (m *MaterializeStage) Open(_ context.Context, upstream View) (View, Meta, io.Closer, error) {
	if upstream.ReaderAt != nil {
		return upstream, Meta{}, nil, nil
	}
	if upstream.Reader == nil {
		return View{}, Meta{}, nil, errors.New("pipeline.MaterializeStage: upstream Reader is nil")
	}
	temp, err := os.CreateTemp("", "cc-port-pipeline-*")
	if err != nil {
		return View{}, Meta{}, nil, fmt.Errorf("materialize: create tempfile: %w", err)
	}
	tempPath := temp.Name()
	if err := os.Chmod(tempPath, 0o600); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return View{}, Meta{}, nil, fmt.Errorf("materialize: chmod tempfile %s: %w", tempPath, err)
	}
	if _, err := io.Copy(temp, upstream.Reader); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return View{}, Meta{}, nil, fmt.Errorf("materialize: drain bytes: %w", err)
	}
	info, err := temp.Stat()
	if err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return View{}, Meta{}, nil, fmt.Errorf("materialize: stat tempfile %s: %w", tempPath, err)
	}
	return View{Reader: temp, ReaderAt: temp, Size: info.Size()},
		Meta{},
		&tempfileCloser{file: temp, path: tempPath},
		nil
}

// Name implements ReaderStage.
func (m *MaterializeStage) Name() string { return "materialize" }

// tempfileCloser closes the tempfile and removes it. errors.Join
// surfaces both failures; os.IsNotExist on Remove is filtered to nil.
// Lives in this package as the single owner of the close-and-remove
// pattern; previous duplicates in internal/encrypt and internal/remote
// were deleted alongside MaterializeStage's introduction.
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
