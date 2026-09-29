package tool_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

func TestRestorer_RestoreAggregatesErrors(t *testing.T) {
	tmp := t.TempDir()
	writablePath := filepath.Join(tmp, "writable", "a.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(writablePath), 0o750))
	require.NoError(t, os.WriteFile(writablePath, []byte("initial"), 0o600))

	restorer := tool.NewRestorer()
	require.NoError(t, restorer.RegisterFile(writablePath), "snapshot writable path before mutation")
	undoErr := errors.New("synthetic rollback failure")
	restorer.RegisterUndo(func() error { return undoErr })

	require.NoError(t, os.WriteFile(writablePath, []byte("mutated"), 0o600))

	err := restorer.Restore()
	require.ErrorIs(t, err, undoErr, "Restore must return a registered undo failure")

	restoredBytes, readErr := os.ReadFile(writablePath) //nolint:gosec // test-controlled path
	require.NoError(t, readErr)
	assert.Equal(t, "initial", string(restoredBytes),
		"writable path must still be restored despite sibling failure")
}

func TestRestorer_RestoreRunsUndosInReverseRegistrationOrder(t *testing.T) {
	restorer := tool.NewRestorer()
	var restored []string
	restorer.RegisterUndo(func() error {
		restored = append(restored, "first")
		return nil
	})
	restorer.RegisterUndo(func() error {
		restored = append(restored, "second")
		return nil
	})

	require.NoError(t, restorer.Restore())

	assert.Equal(t, []string{"second", "first"}, restored)
}

func TestRestorer_RollbackFromSiblingBackup(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "history.jsonl")
	original := []byte(strings.Repeat("abc\n", 300_000)) // >1 MiB: forces the sibling-backup path
	require.NoError(t, os.WriteFile(target, original, 0o600))

	restorer := tool.NewRestorer()
	require.NoError(t, restorer.RegisterFile(target))

	// Overwrite to simulate a partial rewrite.
	require.NoError(t, os.WriteFile(target, []byte("damaged"), 0o600))

	require.NoError(t, restorer.Restore())

	got, err := os.ReadFile(target) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, original, got)
}

// TestRestorer_RestoresMtime covers both RegisterFile branches: a small
// file held in memory, and a large file (>1 MiB, no injectable threshold
// exists on Restorer, so this test writes a real file past it) routed
// through the sibling-backup path. Both must restore the file's original,
// pre-mutation modification time, not the time the restore write happened.
func TestRestorer_RestoresMtime(t *testing.T) {
	t.Run("in-memory branch", func(t *testing.T) {
		tmp := t.TempDir()
		target := filepath.Join(tmp, "small.txt")
		require.NoError(t, os.WriteFile(target, []byte("original"), 0o600))
		past := time.Date(2020, time.March, 1, 12, 0, 0, 0, time.UTC)
		require.NoError(t, os.Chtimes(target, past, past))

		restorer := tool.NewRestorer()
		require.NoError(t, restorer.RegisterFile(target))
		require.NoError(t, os.WriteFile(target, []byte("mutated"), 0o600))

		require.NoError(t, restorer.Restore())

		info, err := os.Stat(target)
		require.NoError(t, err)
		assert.WithinDuration(t, past, info.ModTime(), time.Second,
			"restore must reapply the pre-mutation mtime, not the restore time")
	})

	t.Run("sibling backup branch", func(t *testing.T) {
		tmp := t.TempDir()
		target := filepath.Join(tmp, "large.txt")
		original := []byte(strings.Repeat("abc\n", 300_000)) // >1 MiB: forces the sibling-backup path
		require.NoError(t, os.WriteFile(target, original, 0o600))
		past := time.Date(2020, time.March, 1, 12, 0, 0, 0, time.UTC)
		require.NoError(t, os.Chtimes(target, past, past))

		restorer := tool.NewRestorer()
		require.NoError(t, restorer.RegisterFile(target))
		require.NoError(t, os.WriteFile(target, []byte("mutated"), 0o600))

		require.NoError(t, restorer.Restore())

		info, err := os.Stat(target)
		require.NoError(t, err)
		assert.WithinDuration(t, past, info.ModTime(), time.Second,
			"restore must reapply the pre-mutation mtime, not the restore time")
	})
}

func TestRestorer_CleanupRemovesSiblingBackupsWithoutRestoring(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "history.jsonl")
	original := []byte(strings.Repeat("abc\n", 300_000)) // >1 MiB: forces the sibling-backup path
	require.NoError(t, os.WriteFile(target, original, 0o600))

	entriesBefore, err := os.ReadDir(tmp)
	require.NoError(t, err)

	restorer := tool.NewRestorer()
	require.NoError(t, restorer.RegisterFile(target))

	entriesAfterRegister, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.Len(t, entriesAfterRegister, len(entriesBefore)+1,
		"registering a large file must create exactly one sibling backup")

	require.NoError(t, os.WriteFile(target, []byte("mutated"), 0o600))

	restorer.Cleanup()

	entriesAfterCleanup, err := os.ReadDir(tmp)
	require.NoError(t, err)
	assert.Len(t, entriesAfterCleanup, len(entriesBefore),
		"Cleanup must remove the sibling backup it created")

	got, err := os.ReadFile(target) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, []byte("mutated"), got, "Cleanup must not restore the target")
}

// TestRestorer_ReplaceFileWritesReplacementAndRollsBack covers the in-memory
// RegisterFile branch ReplaceFile drives: it must write the replacement
// bytes, and a later Restore must put back the original bytes, mode, and
// modification time. The replacement mode differs from the original so the
// mode assertion cannot pass by accident.
func TestRestorer_ReplaceFileWritesReplacementAndRollsBack(t *testing.T) {
	original := []byte("original\n")
	replacement := []byte("replacement\n")
	const replacementMode = 0o640
	past := time.Date(2020, time.March, 1, 12, 0, 0, 0, time.UTC)

	target := filepath.Join(t.TempDir(), "target.txt")
	require.NoError(t, os.WriteFile(target, original, 0o600))
	require.NoError(t, os.Chtimes(target, past, past))

	restorer := tool.NewRestorer()
	changed, err := restorer.ReplaceFile(target, original, replacement, replacementMode)
	require.NoError(t, err)
	require.True(t, changed, "differing bytes must be reported as a change")

	written, err := os.ReadFile(target) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, replacement, written, "ReplaceFile must write the replacement bytes")

	require.NoError(t, restorer.Restore())

	restored, err := os.ReadFile(target) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, original, restored, "Restore must put back the original bytes")

	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"Restore must put back the original mode")
	assert.WithinDuration(t, past, info.ModTime(), time.Second,
		"Restore must reapply the pre-mutation mtime, not the restore time")
}

// TestRestorer_ReplaceFileLeavesIdenticalBytesUntouched guards the change
// test ReplaceFile owns: identical bytes must neither be written (a rename
// would give the file a new inode and mtime) nor registered (a later Restore
// would replace a file the move never modified).
func TestRestorer_ReplaceFileLeavesIdenticalBytesUntouched(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"cwd":"/Users/test/Projects/otherproject"}`)
	require.NoError(t, os.WriteFile(target, original, 0o600))
	past := time.Date(2020, time.March, 1, 12, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(target, past, past))
	before, err := os.Stat(target)
	require.NoError(t, err)
	restorer := tool.NewRestorer()

	changed, err := restorer.ReplaceFile(target, original, bytes.Clone(original), 0o600)
	require.NoError(t, err)
	afterReplace, err := os.Stat(target)
	require.NoError(t, err)
	externalEdit := []byte(`{"cwd":"/Users/test/Projects/edited-later"}`)
	require.NoError(t, os.WriteFile(target, externalEdit, 0o600))
	require.NoError(t, restorer.Restore())

	assert.False(t, changed)
	assert.True(t, os.SameFile(before, afterReplace), "an unchanged file keeps its inode")
	assert.Equal(t, before.ModTime(), afterReplace.ModTime())
	restored, err := os.ReadFile(target) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, externalEdit, restored, "Restore has no snapshot of an unchanged file to put back")
}

func TestRestorer_ReplacePathInFileRewritesAndRegisters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"cwd":"/old/proj"}`), 0o644)) //nolint:gosec // G306: test fixture in t.TempDir

	restorer := tool.NewRestorer()
	count, err := restorer.ReplacePathInFile(path, "/old/proj", "/new/proj")
	require.NoError(t, err)
	require.Equal(t, 1, count, "one occurrence must be replaced")

	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err)
	require.JSONEq(t, `{"cwd":"/new/proj"}`, string(got))

	// The registered snapshot must let a Restore reverse the rewrite,
	// proving ReplacePathInFile registered exactly the file it rewrote.
	require.NoError(t, restorer.Restore())
	restored, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err)
	require.JSONEq(t, `{"cwd":"/old/proj"}`, string(restored))
}

func TestRestorer_ReplacePathInFileNoReplacementIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.json")
	original := []byte(`{"unrelated":"content"}`)
	require.NoError(t, os.WriteFile(path, original, 0o644)) //nolint:gosec // G306: test fixture in t.TempDir

	count, err := tool.NewRestorer().ReplacePathInFile(path, "/old/proj", "/new/proj")
	require.NoError(t, err)
	require.Equal(t, 0, count, "no occurrence must be replaced")

	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err)
	require.Equal(t, original, got, "contents must not change")
}

func TestRestorer_ReplacePathInFileMissingFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")

	_, err := tool.NewRestorer().ReplacePathInFile(path, "/old", "/new")
	require.Error(t, err, "expected error for missing file")
}

func TestRestorer_ReplacePathInFileWriteFailsInReadOnlyDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; chmod 0500 will not prevent writes")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"cwd":"/old/proj"}`), 0o644)) //nolint:gosec // G306: test fixture in t.TempDir

	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // G302: deliberately read-only for the test
		t.Skipf("chmod unsupported: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o700) //nolint:gosec // G302: restore perms in test teardown
	})

	// Verify chmod is effective: attempt to create a file.
	probe := filepath.Join(dir, ".probe")
	if f, err := os.Create(probe); err == nil { //nolint:gosec // G304: path from t.TempDir
		_ = f.Close()
		_ = os.Remove(probe)
		t.Skip("chmod 0500 did not prevent writes on this filesystem")
	}

	_, err := tool.NewRestorer().ReplacePathInFile(path, "/old/proj", "/new/proj")
	require.Error(t, err, "expected error writing into read-only dir")
}
