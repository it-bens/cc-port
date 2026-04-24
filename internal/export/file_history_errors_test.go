package export_test

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/testutil"
)

func TestExport_FileHistoryFailsWhenSnapshotUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based fault injection not supported on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("chmod bits bypassed when running as root")
	}

	claudeHome := testutil.SetupFixture(t)

	locations, err := claude.LocateProject(claudeHome, fixtureProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, locations.FileHistoryDirs)
	sort.Strings(locations.FileHistoryDirs)
	firstDir := locations.FileHistoryDirs[0]

	dirEntries, err := os.ReadDir(firstDir)
	require.NoError(t, err)
	require.NotEmpty(t, dirEntries)
	sort.Slice(dirEntries, func(i, j int) bool { return dirEntries[i].Name() < dirEntries[j].Name() })
	snapshotPath := filepath.Join(firstDir, dirEntries[0].Name())

	chmodScoped(t, snapshotPath, 0)

	outputPath := filepath.Join(t.TempDir(), "export.zip")
	_, err = export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   outputPath,
		Categories:   manifest.CategorySet{FileHistory: true},
		Placeholders: defaultPlaceholders(),
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "open ")
	require.ErrorContains(t, err, snapshotPath)
}

func TestExport_FileHistoryFailsWhenDirUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based fault injection not supported on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("chmod bits bypassed when running as root")
	}

	claudeHome := testutil.SetupFixture(t)

	locations, err := claude.LocateProject(claudeHome, fixtureProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, locations.FileHistoryDirs)
	sort.Strings(locations.FileHistoryDirs)
	firstDir := locations.FileHistoryDirs[0]

	chmodScoped(t, firstDir, 0)

	outputPath := filepath.Join(t.TempDir(), "export.zip")
	_, err = export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   outputPath,
		Categories:   manifest.CategorySet{FileHistory: true},
		Placeholders: defaultPlaceholders(),
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "read directory")
}

func TestExport_FileHistoryFailsOnZipWrite(t *testing.T) {
	sentinel := errors.New("synthetic file-history write failure")

	claudeHome := testutil.SetupFixture(t)

	locations, err := claude.LocateProject(claudeHome, fixtureProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, locations.FileHistoryDirs)
	sort.Strings(locations.FileHistoryDirs)
	firstDir := locations.FileHistoryDirs[0]

	dirEntries, err := os.ReadDir(firstDir)
	require.NoError(t, err)
	require.NotEmpty(t, dirEntries)
	sort.Slice(dirEntries, func(i, j int) bool { return dirEntries[i].Name() < dirEntries[j].Name() })
	bigSnapshotPath := filepath.Join(firstDir, dirEntries[0].Name())

	// zip.Writer wraps output in a 4 KiB bufio.Writer, so a fixture whose
	// total compressed output fits in one buffer collapses every entry's
	// flush into archiveWriter.Close. That masks writeReaderToZip's
	// per-entry error-wrap and surfaces failures as "finalize archive"
	// regardless of where they occurred. Overwriting the lex-first
	// file-history snapshot with 64 KiB of crypto/rand bytes makes the
	// body incompressible, so deflate emits near-1:1 output and forces
	// multiple bufio spills mid entry.Write. The sentinel then fires
	// inside the spill and surfaces through the "write zip entry
	// file-history/..." wrap.
	bigBody := make([]byte, 64<<10)
	_, err = crand.Read(bigBody)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bigSnapshotPath, bigBody, 0o600))

	siblingHome := testutil.SetupFixture(t)
	siblingPath := filepath.Join(t.TempDir(), "sibling.zip")
	_, err = export.Run(t.Context(), siblingHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   siblingPath,
		Categories:   manifest.CategorySet{},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err, "sibling export for threshold discovery must succeed")

	siblingInfo, err := os.Stat(siblingPath)
	require.NoError(t, err)
	limitBytes := int(siblingInfo.Size()) + 64

	opener := func(path string) (io.WriteCloser, error) {
		realFile, err := os.Create(path) //nolint:gosec // G304: test-controlled tempdir path
		if err != nil {
			return nil, err
		}
		return &writeLimitCloser{inner: realFile, limit: limitBytes, writeErr: sentinel}, nil
	}

	outputPath := filepath.Join(t.TempDir(), "out.zip")

	_, err = export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   outputPath,
		Categories:   manifest.CategorySet{FileHistory: true},
		Placeholders: defaultPlaceholders(),
	}, export.WithArchiveOpener(opener))

	require.Error(t, err, "Run must surface the synthetic write failure")
	require.ErrorIs(t, err, sentinel, "the sentinel must be in the error chain")
	require.ErrorContains(t, err, "file-history/",
		"the failure must wrap a file-history archive entry, not metadata or another category")
}

func TestExport_FileHistoryHonorsContextCancelMidWalk(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	locations, err := claude.LocateProject(claudeHome, fixtureProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, locations.FileHistoryDirs)
	sort.Strings(locations.FileHistoryDirs)
	firstDir := locations.FileHistoryDirs[0]

	dirEntries, err := os.ReadDir(firstDir)
	require.NoError(t, err)
	require.NotEmpty(t, dirEntries)
	sort.Slice(dirEntries, func(i, j int) bool { return dirEntries[i].Name() < dirEntries[j].Name() })
	bigSnapshotPath := filepath.Join(firstDir, dirEntries[0].Name())

	// zip.Writer wraps output in a 4 KiB bufio.Writer, so a sub-4 KiB
	// fixture collapses every entry's flush into archiveWriter.Close.
	// That hides the mid-walk ctx cancel because the marker-trigger
	// closer sees no bytes until after exportFileHistory has already
	// returned. 64 KiB of crypto/rand bytes is incompressible, so
	// deflate emits near-1:1 output and forces mid-entry bufio spills.
	// The "file-history/" substring reaches the closer while the walk
	// is still iterating; cancel fires, the next ctx check returns.
	bigBody := make([]byte, 64<<10)
	_, err = crand.Read(bigBody)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bigSnapshotPath, bigBody, 0o600))

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	outputPath := filepath.Join(t.TempDir(), "out.zip")
	opener := func(path string) (io.WriteCloser, error) {
		realFile, err := os.Create(path) //nolint:gosec // G304: test-controlled tempdir path
		if err != nil {
			return nil, err
		}
		return &cancelOnMarkerCloser{
			inner:  realFile,
			marker: []byte("file-history/"),
			cancel: cancel,
		}, nil
	}

	_, err = export.Run(ctx, claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   outputPath,
		Categories:   manifest.CategorySet{FileHistory: true},
		Placeholders: defaultPlaceholders(),
	}, export.WithArchiveOpener(opener))

	require.Error(t, err, "Run must surface the ctx cancel triggered after the first file-history write")
	require.ErrorIs(t, err, context.Canceled, "context.Canceled must be in the error chain")
}

// chmodScoped sets path's mode and registers a Cleanup to restore the
// captured pre-chmod mode. Restoration runs before t.TempDir's rm-rf so
// fixture cleanup succeeds even if chmod 000 made the entry unreadable.
func chmodScoped(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err, "stat before chmod %s", path)
	t.Cleanup(func() { _ = os.Chmod(path, info.Mode()) })
	require.NoError(t, os.Chmod(path, mode), "chmod %s to %#o", path, mode)
}

// cancelOnMarkerCloser passes writes through to inner and calls cancel
// once the cumulative bytes seen contain marker. Used to drive ctx-mid-walk
// cancellation tests against zip writes whose timing depends on entry size.
type cancelOnMarkerCloser struct {
	inner  *os.File
	marker []byte
	seen   []byte
	cancel context.CancelFunc
	fired  bool
}

func (c *cancelOnMarkerCloser) Write(p []byte) (int, error) {
	n, err := c.inner.Write(p)
	if !c.fired {
		c.seen = append(c.seen, p[:n]...)
		if bytes.Contains(c.seen, c.marker) {
			c.fired = true
			c.cancel()
		}
	}
	return n, err
}

func (c *cancelOnMarkerCloser) Close() error { return c.inner.Close() }
