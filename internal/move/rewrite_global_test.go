package move

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRewriteTracked_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	//nolint:gosec // G306: test fixture in t.TempDir
	if err := os.WriteFile(path, []byte(`{"cwd":"/old/proj"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tracker := &globalFileTracker{}
	if err := rewriteTracked(path, "/old/proj", "/new/proj", tracker); err != nil {
		t.Fatalf("rewriteTracked: %v", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != `{"cwd":"/new/proj"}` {
		t.Fatalf("unexpected contents: %q", string(got))
	}
	if len(tracker.saved) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(tracker.saved))
	}
}

func TestRewriteTracked_SaveFails_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")

	tracker := &globalFileTracker{}
	if err := rewriteTracked(path, "/old", "/new", tracker); err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
}

func TestRewriteTracked_NoReplacement_NoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	original := []byte(`{"unrelated":"content"}`)
	if err := os.WriteFile(path, original, 0o644); err != nil { //nolint:gosec // G306: test fixture in t.TempDir
		t.Fatalf("seed: %v", err)
	}

	tracker := &globalFileTracker{}
	if err := rewriteTracked(path, "/old/proj", "/new/proj", tracker); err != nil {
		t.Fatalf("rewriteTracked: %v", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("contents changed: %q", string(got))
	}
}

func TestRewriteTracked_WriteFails_ReadOnlyDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; chmod 0500 will not prevent writes")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	//nolint:gosec // G306: test fixture in t.TempDir
	if err := os.WriteFile(path, []byte(`{"cwd":"/old/proj"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

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

	tracker := &globalFileTracker{}
	if err := rewriteTracked(path, "/old/proj", "/new/proj", tracker); err == nil {
		t.Fatalf("expected error writing into read-only dir, got nil")
	}
}
