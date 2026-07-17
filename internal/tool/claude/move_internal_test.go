package claude

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

func TestRewriteTracked_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"cwd":"/old/proj"}`), 0o644)) //nolint:gosec // G306: test fixture in t.TempDir

	restorer := tool.NewRestorer()
	count, err := rewriteTracked(path, "/old/proj", "/new/proj", restorer)
	require.NoError(t, err)
	require.Equal(t, 1, count, "one occurrence must be replaced")

	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err)
	require.JSONEq(t, `{"cwd":"/new/proj"}`, string(got))

	// The registered snapshot must let a Restore reverse the rewrite,
	// proving rewriteTracked registered exactly the file it rewrote.
	require.NoError(t, restorer.Restore())
	restored, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err)
	require.JSONEq(t, `{"cwd":"/old/proj"}`, string(restored))
}

func TestRewriteTracked_SaveFails_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")

	_, err := rewriteTracked(path, "/old", "/new", tool.NewRestorer())
	require.Error(t, err, "expected error for missing file")
}

func TestRewriteTracked_NoReplacement_NoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	original := []byte(`{"unrelated":"content"}`)
	require.NoError(t, os.WriteFile(path, original, 0o644)) //nolint:gosec // G306: test fixture in t.TempDir

	count, err := rewriteTracked(path, "/old/proj", "/new/proj", tool.NewRestorer())
	require.NoError(t, err)
	require.Equal(t, 0, count, "no occurrence must be replaced")

	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err)
	require.True(t, bytes.Equal(got, original), "contents must not change")
}

func TestRewriteTracked_WriteFails_ReadOnlyDir(t *testing.T) {
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

	_, err := rewriteTracked(path, "/old/proj", "/new/proj", tool.NewRestorer())
	require.Error(t, err, "expected error writing into read-only dir")
}
