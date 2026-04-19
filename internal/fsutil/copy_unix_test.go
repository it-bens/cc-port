//go:build linux || darwin

package fsutil

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyDir_RejectsIrregularEntry(t *testing.T) {
	source := t.TempDir()
	fifoPath := filepath.Join(source, "pipe")
	if err := syscall.Mkfifo(fifoPath, 0o644); err != nil {
		t.Skipf("cannot create FIFO in test environment: %v", err)
	}

	destination := filepath.Join(t.TempDir(), "dst")
	err := CopyDir(source, destination)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "irregular")
}
