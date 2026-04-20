package move

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGlobalFileTracker_RestoreAggregatesErrors(t *testing.T) {
	tmp := t.TempDir()
	writablePath := filepath.Join(tmp, "writable", "a.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(writablePath), 0o750))
	require.NoError(t, os.WriteFile(writablePath, []byte("initial"), 0o600))

	readOnlyDir := filepath.Join(tmp, "readonly")
	require.NoError(t, os.MkdirAll(readOnlyDir, 0o750))
	readOnlyPath := filepath.Join(readOnlyDir, "b.txt")
	require.NoError(t, os.WriteFile(readOnlyPath, []byte("initial"), 0o600))
	// Strip write permission from the parent so SafeWriteFile's
	// rename-staging cannot land a new file at readOnlyPath.
	require.NoError(t, os.Chmod(readOnlyDir, 0o500)) //nolint:gosec // G302: read+exec only is the whole point
	t.Cleanup(func() {
		_ = os.Chmod(readOnlyDir, 0o700) //nolint:gosec // G302: restore for t.TempDir cleanup
	})

	tracker := &globalFileTracker{
		saved: []savedFile{
			{path: writablePath, data: []byte("restored"), mode: 0o600},
			{path: readOnlyPath, data: []byte("restored"), mode: 0o600},
		},
	}

	err := tracker.restore()
	require.Error(t, err, "restore must surface the read-only-path failure")
	require.ErrorContains(t, err, readOnlyPath)

	restoredBytes, readErr := os.ReadFile(writablePath) //nolint:gosec // test-controlled path
	require.NoError(t, readErr)
	require.Equal(t, "restored", string(restoredBytes),
		"writable path must still be restored despite sibling failure")
}
