package testmain_test

import (
	"errors"
	"io"
	"io/fs"
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

func TestStageTempDir_StagesIntoDirectoryAndCleanupRemovesIt(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	var stagedDir string

	cleanup, err := testmain.StageTempDir("cc-port-stage-", func(dir string) error {
		stagedDir = dir
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, stagedDir)
	info, statErr := os.Stat(stagedDir)
	require.NoError(t, statErr, "the directory stage received must exist after the call")
	require.True(t, info.IsDir())

	require.NoError(t, cleanup())
	_, statErr = os.Stat(stagedDir)
	assert.True(t, os.IsNotExist(statErr), "the staged directory must be removed after cleanup")
}

func TestStageTempDir_StageErrorReturnsItAndRemovesDirectory(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	stageErr := errors.New("stage fixture")
	var stagedDir string

	cleanup, err := testmain.StageTempDir("cc-port-stage-", func(dir string) error {
		stagedDir = dir
		return stageErr
	})

	require.ErrorIs(t, err, stageErr)
	assert.Nil(t, cleanup)
	require.NotEmpty(t, stagedDir)
	_, statErr := os.Stat(stagedDir)
	assert.True(t, os.IsNotExist(statErr), "the staged directory must be removed after a stage error")
}

func TestStageTempDir_CreateErrorReturnsItWithoutStaging(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	stageCalled := false

	cleanup, err := testmain.StageTempDir("cc-port-stage-", func(_ string) error {
		stageCalled = true
		return nil
	})

	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Contains(t, err.Error(), "create temporary directory")
	assert.Nil(t, cleanup)
	assert.False(t, stageCalled, "stage must not be called when the directory cannot be created")
}

func TestStageTempDir_CleanupReturnsRemovalError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits, so removal cannot be made to fail")
	}
	parentDir := t.TempDir()
	t.Setenv("TMPDIR", parentDir)

	cleanup, err := testmain.StageTempDir("cc-port-stage-", func(dir string) error {
		return os.WriteFile(filepath.Join(dir, "fixture.txt"), []byte("fixture"), 0o600)
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.Chmod(parentDir, 0o700)) //nolint:gosec // G302: directory under t.TempDir().
	})
	require.NoError(t, os.Chmod(parentDir, 0o500)) //nolint:gosec // G302: read-only directory makes removal fail.

	returnedErr := cleanup()

	require.ErrorIs(t, returnedErr, fs.ErrPermission)
	assert.Contains(t, returnedErr.Error(), "remove temporary directory")
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

func TestRemoveAfterFailure_ReturnsSetupErrorAndRemovalErrorWhenRemovalFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits, so removal cannot be made to fail")
	}
	parent := filepath.Join(t.TempDir(), "staged-parent")
	dir := filepath.Join(parent, "staged-fixture")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture.txt"), []byte("fixture"), 0o600))
	t.Cleanup(func() {
		require.NoError(t, os.Chmod(parent, 0o700)) //nolint:gosec // G302: directory under t.TempDir().
	})
	require.NoError(t, os.Chmod(parent, 0o500)) //nolint:gosec // G302: read-only directory makes removal fail.
	setupErr := errors.New("stage fixture")

	returnedErr := testmain.RemoveAfterFailure(dir, setupErr)

	require.ErrorIs(t, returnedErr, setupErr)
	require.ErrorIs(t, returnedErr, fs.ErrPermission)
	assert.Contains(t, returnedErr.Error(), "remove temporary directory after failed setup")
}
