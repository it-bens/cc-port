package fsutil

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// dirModeAfterUmask creates a reference directory at the requested mode under
// tempDir and returns the actual permission bits the kernel applied. This
// makes mode assertions umask-agnostic: the copy only needs to match what the
// kernel would have produced for an ordinary MkdirAll at the same mode.
func dirModeAfterUmask(t *testing.T, tempDir string, name string, mode os.FileMode) os.FileMode {
	t.Helper()
	referencePath := filepath.Join(tempDir, name)
	if err := os.Mkdir(referencePath, mode); err != nil {
		t.Fatalf("create reference dir %q: %v", referencePath, err)
	}
	info, err := os.Stat(referencePath)
	if err != nil {
		t.Fatalf("stat reference dir %q: %v", referencePath, err)
	}
	return info.Mode().Perm()
}

func fileModeAfterUmask(t *testing.T, tempDir string, name string, mode os.FileMode) os.FileMode {
	t.Helper()
	referencePath := filepath.Join(tempDir, name)
	if err := os.WriteFile(referencePath, []byte("ref"), mode); err != nil {
		t.Fatalf("create reference file %q: %v", referencePath, err)
	}
	info, err := os.Stat(referencePath)
	if err != nil {
		t.Fatalf("stat reference file %q: %v", referencePath, err)
	}
	return info.Mode().Perm()
}

func TestCopyDirPreservesDirectoryMode0755(t *testing.T) {
	referenceRoot := t.TempDir()
	expected := dirModeAfterUmask(t, referenceRoot, "ref", 0o755)

	source := t.TempDir()
	sub := filepath.Join(source, "child")
	if err := os.Mkdir(sub, 0o755); err != nil { //nolint:gosec // G301: asserting mode preservation end-to-end
		t.Fatalf("mkdir source child: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "dst")
	if err := CopyDir(t.Context(), source, destination); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}

	info, err := os.Stat(filepath.Join(destination, "child"))
	if err != nil {
		t.Fatalf("stat copied child: %v", err)
	}
	if got := info.Mode().Perm(); got != expected {
		t.Fatalf("child mode = %o, want %o", got, expected)
	}
}

func TestCopyDirPreservesDirectoryMode0700(t *testing.T) {
	referenceRoot := t.TempDir()
	expected := dirModeAfterUmask(t, referenceRoot, "ref", 0o700)

	source := t.TempDir()
	sub := filepath.Join(source, "secret")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatalf("mkdir source child: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "dst")
	if err := CopyDir(t.Context(), source, destination); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}

	info, err := os.Stat(filepath.Join(destination, "secret"))
	if err != nil {
		t.Fatalf("stat copied child: %v", err)
	}
	if got := info.Mode().Perm(); got != expected {
		t.Fatalf("child mode = %o, want %o", got, expected)
	}
}

func TestCopyDirPreservesNestedMixedDirectoryModes(t *testing.T) {
	referenceRoot := t.TempDir()
	expectedParent := dirModeAfterUmask(t, referenceRoot, "parent-ref", 0o755)
	expectedChild := dirModeAfterUmask(t, referenceRoot, "child-ref", 0o700)

	source := t.TempDir()
	parent := filepath.Join(source, "parent")
	if err := os.Mkdir(parent, 0o755); err != nil { //nolint:gosec // G301: asserting mode preservation end-to-end
		t.Fatalf("mkdir parent: %v", err)
	}
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "dst")
	if err := CopyDir(t.Context(), source, destination); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}

	parentInfo, err := os.Stat(filepath.Join(destination, "parent"))
	if err != nil {
		t.Fatalf("stat copied parent: %v", err)
	}
	if got := parentInfo.Mode().Perm(); got != expectedParent {
		t.Fatalf("parent mode = %o, want %o", got, expectedParent)
	}

	childInfo, err := os.Stat(filepath.Join(destination, "parent", "child"))
	if err != nil {
		t.Fatalf("stat copied child: %v", err)
	}
	if got := childInfo.Mode().Perm(); got != expectedChild {
		t.Fatalf("child mode = %o, want %o", got, expectedChild)
	}
}

func TestCopyDirPreservesFileMode(t *testing.T) {
	referenceRoot := t.TempDir()
	expectedExec := fileModeAfterUmask(t, referenceRoot, "exec-ref", 0o755)
	expectedReadOnly := fileModeAfterUmask(t, referenceRoot, "ro-ref", 0o400)

	source := t.TempDir()
	execPath := filepath.Join(source, "run.sh")
	// #nosec G306 -- asserting mode preservation end-to-end
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write exec source: %v", err)
	}
	readOnlyPath := filepath.Join(source, "locked.txt")
	if err := os.WriteFile(readOnlyPath, []byte("ro"), 0o400); err != nil {
		t.Fatalf("write ro source: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "dst")
	if err := CopyDir(t.Context(), source, destination); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}

	execInfo, err := os.Stat(filepath.Join(destination, "run.sh"))
	if err != nil {
		t.Fatalf("stat copied exec: %v", err)
	}
	if got := execInfo.Mode().Perm(); got != expectedExec {
		t.Fatalf("exec mode = %o, want %o", got, expectedExec)
	}

	readOnlyInfo, err := os.Stat(filepath.Join(destination, "locked.txt"))
	if err != nil {
		t.Fatalf("stat copied ro file: %v", err)
	}
	if got := readOnlyInfo.Mode().Perm(); got != expectedReadOnly {
		t.Fatalf("ro mode = %o, want %o", got, expectedReadOnly)
	}
}

// closeErroringCloser wraps a real *os.File: writes pass through unchanged,
// but Close() closes the real handle (to avoid leaks) and returns a synthetic
// error. Tests use this to prove deferred Close errors propagate out of the
// caller.
type closeErroringCloser struct {
	io.Writer
	realFile io.Closer
	closeErr error
}

func (c *closeErroringCloser) Close() error {
	_ = c.realFile.Close()
	return c.closeErr
}

func TestCopyDir_CloseErrorPropagates(t *testing.T) {
	originalFactory := openStreamDest
	t.Cleanup(func() { openStreamDest = originalFactory })

	sentinel := errors.New("synthetic close failure")
	openStreamDest = func(root *os.Root, path string, mode os.FileMode) (io.WriteCloser, error) {
		realFile, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return nil, err
		}
		return &closeErroringCloser{Writer: realFile, realFile: realFile, closeErr: sentinel}, nil
	}

	src := t.TempDir()
	dst := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "f.txt"), []byte("payload"), 0o600))

	err := CopyDir(t.Context(), src, dst)
	require.Error(t, err, "CopyDir must surface the deferred close error")
	require.ErrorIs(t, err, sentinel, "the synthetic sentinel must be in the error chain")
	require.ErrorContains(t, err, "close destination")
}

func TestCopyDir_AbortsOnCancelledContext(t *testing.T) {
	// Arrange: source dir with one regular file so the WalkDir callback has
	// at least one non-root entry to iterate; the copy must therefore pass
	// through a callback invocation where the ctx check can fire.
	source := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(source, "source-with-one-file"), []byte("payload"), 0o600,
	))
	destination := filepath.Join(t.TempDir(), "destination")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Act
	err := CopyDir(ctx, source, destination)

	// Assert
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}
