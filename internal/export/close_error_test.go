package export_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/testutil"
)

// writeLimitCloser passes the first limit bytes through to inner then fails
// every subsequent Write. Close closes the inner handle. Used to force
// zip.Writer.Close (which writes the central directory) to error while the
// underlying file close still succeeds. The writer also serves as the
// per-entry write-fault driver for file-history tests.
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

// closeErrorOptions builds export.Options for the writer-close fault test.
// The Output field is left nil; callers populate it with the fault writer
// before calling export.Run. fixtureProjectPath is declared in
// export_test.go (same package).
func closeErrorOptions() *export.Options {
	return &export.Options{
		ProjectPath: fixtureProjectPath,
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

// TestRun_ArchiveWriterCloseError proves that an error returned by
// zip.Writer.Close (the central-directory flush) propagates out of Run
// wrapped as "finalize archive". The fault writer accepts the first 4 KiB
// — generous headroom for headers and bodies from the minimal fixture —
// and fails every subsequent write, which is the deflate that
// zip.Writer.Close issues.
func TestRun_ArchiveWriterCloseError(t *testing.T) {
	sentinel := errors.New("synthetic archive-writer close failure")
	realFile, err := os.Create(filepath.Join(t.TempDir(), "underlying.zip"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = realFile.Close() })

	faultWriter := &writeLimitCloser{inner: realFile, limit: 4096, writeErr: sentinel}

	claudeHome := testutil.SetupFixture(t)
	options := closeErrorOptions()
	options.Output = faultWriter

	_, err = export.Run(t.Context(), claudeHome, options)

	require.Error(t, err, "Run must surface the deferred archive-writer close error")
	require.ErrorIs(t, err, sentinel, "the synthetic sentinel must be in the error chain")
	require.ErrorContains(t, err, "finalize archive")
}
