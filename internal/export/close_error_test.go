package export_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/testutil"
)

// closeErroringCloser wraps a real *os.File: writes pass through unchanged,
// Close closes the real handle to release the fd and returns a synthetic
// error. Used to prove deferred Close errors propagate out of Run.
type closeErroringCloser struct {
	io.Writer
	realFile io.Closer
	closeErr error
}

func (c *closeErroringCloser) Close() error {
	_ = c.realFile.Close()
	return c.closeErr
}

// writeLimitCloser passes the first limit bytes through to inner then fails
// every subsequent Write. Close closes the inner handle. Used to force
// zip.Writer.Close (which writes the central directory) to error while the
// underlying file close still succeeds.
type writeLimitCloser struct {
	inner    *os.File
	limit    int
	written  int
	writeErr error
}

func (w *writeLimitCloser) Write(p []byte) (int, error) {
	remaining := w.limit - w.written
	if remaining <= 0 {
		return 0, w.writeErr
	}
	if len(p) <= remaining {
		n, err := w.inner.Write(p)
		w.written += n
		return n, err
	}
	n, err := w.inner.Write(p[:remaining])
	w.written += n
	if err != nil {
		return n, err
	}
	return n, w.writeErr
}

func (w *writeLimitCloser) Close() error {
	return w.inner.Close()
}

// closeErrorOptions builds export.Options for the close-error tests.
// fixtureProjectPath is declared in export_test.go (same package).
func closeErrorOptions(outputPath string) *export.Options {
	return &export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories: manifest.CategorySet{
			Sessions: true,
			Memory:   true,
		},
		Placeholders: []manifest.Placeholder{
			{Key: "{{PROJECT_PATH}}", Original: fixtureProjectPath},
			{Key: "{{HOME}}", Original: "/Users/test"},
		},
	}
}

func TestRun_ArchiveFileCloseError(t *testing.T) {
	sentinel := errors.New("synthetic archive-file close failure")
	opener := func(path string) (io.WriteCloser, error) {
		realFile, err := os.Create(path) //nolint:gosec // G304: test-controlled tempdir path
		if err != nil {
			return nil, err
		}
		return &closeErroringCloser{Writer: realFile, realFile: realFile, closeErr: sentinel}, nil
	}

	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")

	_, err := export.Run(t.Context(), claudeHome, closeErrorOptions(outputPath), export.WithArchiveOpener(opener))

	require.Error(t, err, "Run must surface the deferred archive-file close error")
	require.ErrorIs(t, err, sentinel, "the synthetic sentinel must be in the error chain")
	require.ErrorContains(t, err, "close archive file")
}

func TestRun_ArchiveWriterCloseError(t *testing.T) {
	sentinel := errors.New("synthetic archive-writer close failure")
	opener := func(path string) (io.WriteCloser, error) {
		realFile, err := os.Create(path) //nolint:gosec // G304: test-controlled tempdir path
		if err != nil {
			return nil, err
		}
		// Let the zip writer emit local file headers and compressed bodies,
		// then fail before zip.Writer.Close can write the central directory.
		// 4 KiB is generous headroom for header-only output from a minimal
		// fixture; zip.Writer.Close always writes at least a few hundred
		// bytes of central-directory records.
		return &writeLimitCloser{inner: realFile, limit: 4096, writeErr: sentinel}, nil
	}

	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")

	_, err := export.Run(t.Context(), claudeHome, closeErrorOptions(outputPath), export.WithArchiveOpener(opener))

	require.Error(t, err, "Run must surface the deferred archive-writer close error")
	require.ErrorIs(t, err, sentinel, "the synthetic sentinel must be in the error chain")
	require.ErrorContains(t, err, "finalize archive")
}
