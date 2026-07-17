package tool_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

func TestRestorer_RestoreAggregatesErrors(t *testing.T) {
	tmp := t.TempDir()
	writablePath := filepath.Join(tmp, "writable", "a.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(writablePath), 0o750))
	require.NoError(t, os.WriteFile(writablePath, []byte("initial"), 0o600))

	readOnlyDir := filepath.Join(tmp, "readonly")
	require.NoError(t, os.MkdirAll(readOnlyDir, 0o750))
	readOnlyPath := filepath.Join(readOnlyDir, "b.txt")
	require.NoError(t, os.WriteFile(readOnlyPath, []byte("initial"), 0o600))

	restorer := tool.NewRestorer()
	require.NoError(t, restorer.RegisterFile(writablePath), "snapshot writable path before mutation")
	require.NoError(t, restorer.RegisterFile(readOnlyPath), "snapshot read-only-dir path before mutation")

	// Mutate both targets in place (truncate+rewrite, no new directory
	// entry), then strip write permission from readOnlyDir so Restore's
	// SafeWriteFile (temp-create + rename, both directory operations)
	// cannot land its restored copy there.
	require.NoError(t, os.WriteFile(writablePath, []byte("mutated"), 0o600))
	require.NoError(t, os.WriteFile(readOnlyPath, []byte("mutated"), 0o600))
	require.NoError(t, os.Chmod(readOnlyDir, 0o500)) //nolint:gosec // G302: read+exec only is the whole point
	t.Cleanup(func() {
		_ = os.Chmod(readOnlyDir, 0o700) //nolint:gosec // G302: restore for t.TempDir cleanup
	})

	err := restorer.Restore()
	require.Error(t, err, "Restore must surface the read-only-path failure")
	require.ErrorContains(t, err, readOnlyDir,
		"the failure must name the read-only directory SafeWriteFile could not write into")

	restoredBytes, readErr := os.ReadFile(writablePath) //nolint:gosec // test-controlled path
	require.NoError(t, readErr)
	assert.Equal(t, "initial", string(restoredBytes),
		"writable path must still be restored despite sibling failure")
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
