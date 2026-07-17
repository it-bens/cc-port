package claude_test

import (
	"archive/zip"
	"bytes"
	"context"
	crand "crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// writeLimitCloser passes the first limit bytes through to inner then fails
// every subsequent Write. Used to force a zip entry write to fail mid-stream
// rather than at Close.
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

// cancelOnMarkerCloser passes writes through to inner and calls cancel once
// the cumulative bytes seen contain marker. Used to drive ctx-mid-walk
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

// firstFileHistorySnapshot locates the first (lexically) file-history
// snapshot the fixture project owns, for direct fault-injection tests
// against the file bytes.
func firstFileHistorySnapshot(t *testing.T, home *claude.Home) (dir, path string) {
	t.Helper()
	locations, err := claude.LocateProject(home, testProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, locations.FileHistoryDirs)
	sort.Strings(locations.FileHistoryDirs)
	firstDir := locations.FileHistoryDirs[0]

	dirEntries, err := os.ReadDir(firstDir)
	require.NoError(t, err)
	require.NotEmpty(t, dirEntries)
	sort.Slice(dirEntries, func(i, j int) bool { return dirEntries[i].Name() < dirEntries[j].Name() })
	return firstDir, filepath.Join(firstDir, dirEntries[0].Name())
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

func TestExport_FileHistoryFailsWhenSnapshotUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based fault injection not supported on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("chmod bits bypassed when running as root")
	}

	home := testutil.SetupFixture(t)
	_, snapshotPath := firstFileHistorySnapshot(t, home)
	chmodScoped(t, snapshotPath, 0)

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	sink := archive.NewSink(writer, "claude", nil)
	workspace := claude.NewWorkspace(home)

	_, err := workspace.Export(context.Background(), testProjectPath, map[string]bool{"file-history": true}, sink)

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

	home := testutil.SetupFixture(t)
	firstDir, _ := firstFileHistorySnapshot(t, home)
	chmodScoped(t, firstDir, 0)

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	sink := archive.NewSink(writer, "claude", nil)
	workspace := claude.NewWorkspace(home)

	_, err := workspace.Export(context.Background(), testProjectPath, map[string]bool{"file-history": true}, sink)

	require.Error(t, err)
	require.ErrorContains(t, err, "read directory")
}

func TestExport_FileHistoryFailsOnZipWrite(t *testing.T) {
	sentinel := errors.New("synthetic file-history write failure")

	home := testutil.SetupFixture(t)
	_, bigSnapshotPath := firstFileHistorySnapshot(t, home)

	// zip.Writer wraps output in a 4 KiB bufio.Writer, so a small fixture
	// collapses every entry's flush into the eventual Close. Overwriting
	// the lex-first file-history snapshot with 64 KiB of crypto/rand bytes
	// makes the body incompressible, so deflate emits near-1:1 output and
	// forces multiple bufio spills mid entry.Write, well before the small
	// local-file-header bytes that precede it.
	bigBody := make([]byte, 64<<10)
	_, err := crand.Read(bigBody)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bigSnapshotPath, bigBody, 0o600))

	realFile, err := os.Create(filepath.Join(t.TempDir(), "out.zip"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = realFile.Close() })
	faultWriter := &writeLimitCloser{inner: realFile, limit: 256, writeErr: sentinel}

	writer := zip.NewWriter(faultWriter)
	sink := archive.NewSink(writer, "claude", nil)
	workspace := claude.NewWorkspace(home)

	_, err = workspace.Export(context.Background(), testProjectPath, map[string]bool{"file-history": true}, sink)

	require.Error(t, err, "Export must surface the synthetic write failure")
	require.ErrorIs(t, err, sentinel, "the sentinel must be in the error chain")
	require.ErrorContains(t, err, "file-history/",
		"the failure must wrap a file-history archive entry")
}

func TestExport_FileHistoryHonorsContextCancelMidWalk(t *testing.T) {
	home := testutil.SetupFixture(t)
	_, bigSnapshotPath := firstFileHistorySnapshot(t, home)

	// Same incompressible-body technique as the write-fault test above,
	// so the "file-history/" marker reaches the closer mid-walk rather
	// than all at once at Close time.
	bigBody := make([]byte, 64<<10)
	_, err := crand.Read(bigBody)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bigSnapshotPath, bigBody, 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	realFile, err := os.Create(filepath.Join(t.TempDir(), "out.zip"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = realFile.Close() })
	faultWriter := &cancelOnMarkerCloser{
		inner:  realFile,
		marker: []byte("file-history/"),
		cancel: cancel,
	}

	writer := zip.NewWriter(faultWriter)
	sink := archive.NewSink(writer, "claude", nil)
	workspace := claude.NewWorkspace(home)

	_, err = workspace.Export(ctx, testProjectPath, map[string]bool{"file-history": true}, sink)

	require.Error(t, err, "Export must surface the ctx cancel triggered after the first file-history write")
	require.ErrorIs(t, err, context.Canceled, "context.Canceled must be in the error chain")
}
