package testmain_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/testmain"
)

// replaceStderr points os.Stderr at a pipe and returns a function the caller
// runs after the act: it closes the write end and returns everything written
// to the pipe.
func replaceStderr(t *testing.T) func() string {
	t.Helper()

	readEnd, writeEnd, err := os.Pipe()
	require.NoError(t, err)
	originalStderr := os.Stderr
	os.Stderr = writeEnd
	t.Cleanup(func() {
		os.Stderr = originalStderr
		_ = readEnd.Close()
	})

	return func() string {
		require.NoError(t, writeEnd.Close())
		captured, err := io.ReadAll(readEnd)
		require.NoError(t, err)
		return string(captured)
	}
}

func TestRun_ReturnsRunExitCode(t *testing.T) {
	exitCode := testmain.Run(
		func() (func() error, error) { return func() error { return nil }, nil },
		func() int { return 3 },
	)

	assert.Equal(t, 3, exitCode)
}

func TestRun_SetupErrorSkipsRun(t *testing.T) {
	readStderr := replaceStderr(t)
	runCalled := false

	exitCode := testmain.Run(
		func() (func() error, error) { return nil, errors.New("stage fixture") },
		func() int {
			runCalled = true
			return 0
		},
	)
	captured := readStderr()

	assert.Equal(t, 1, exitCode)
	assert.False(t, runCalled, "run must not be called after a setup error")
	assert.Contains(t, captured, "stage fixture")
}

func TestRun_CleanupErrorReturnsOne(t *testing.T) {
	readStderr := replaceStderr(t)

	exitCode := testmain.Run(
		func() (func() error, error) {
			return func() error { return errors.New("remove fixture") }, nil
		},
		func() int { return 0 },
	)
	captured := readStderr()

	assert.Equal(t, 1, exitCode)
	assert.Contains(t, captured, "remove fixture")
}

func TestRun_CleansUpAfterRun(t *testing.T) {
	var steps []string

	testmain.Run(
		func() (func() error, error) {
			steps = append(steps, "setup")
			return func() error {
				steps = append(steps, "cleanup")
				return nil
			}, nil
		},
		func() int {
			steps = append(steps, "run")
			return 0
		},
	)

	assert.Equal(t, []string{"setup", "run", "cleanup"}, steps)
}

func TestRemoveAfterFailure_RemovesDirectoryAndReturnsError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "staged-fixture")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	setupErr := errors.New("stage fixture")

	returnedErr := testmain.RemoveAfterFailure(dir, setupErr)

	require.ErrorIs(t, returnedErr, setupErr)
	_, statErr := os.Stat(dir)
	assert.True(t, os.IsNotExist(statErr), "the staged directory must be removed after a failed setup")
}
