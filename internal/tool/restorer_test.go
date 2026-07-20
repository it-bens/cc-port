package tool_test

import (
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
