// Package testmain provides the setup, run and cleanup sequence a package's
// TestMain needs around *testing.M.Run.
package testmain

import (
	"errors"
	"fmt"
	"os"
)

// Run calls setup, then run, then the cleanup setup returned, and returns the
// exit code for TestMain to pass to os.Exit. A setup error prints to stderr
// and yields exit code 1 without running run. A cleanup error prints to stderr
// and yields exit code 1. Otherwise Run returns run's exit code.
func Run(setup func() (cleanup func() error, err error), run func() int) int {
	cleanup, err := setup()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	exitCode := run()
	if err := cleanup(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return exitCode
}

// StageTempDir creates a temporary directory whose name includes pattern, calls
// stage with it, and returns a cleanup function that removes the directory. If
// stage fails, StageTempDir removes the directory and returns a nil cleanup and
// the stage error, joined with the removal error when the removal fails too.
func StageTempDir(pattern string, stage func(dir string) error) (cleanup func() error, err error) {
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		return nil, fmt.Errorf("create temporary directory: %w", err)
	}
	if err = stage(dir); err != nil {
		return nil, RemoveAfterFailure(dir, err)
	}
	return func() error {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove temporary directory: %w", err)
		}
		return nil
	}, nil
}

// RemoveAfterFailure removes dir after a failed setup step and returns err,
// joined with the removal error when the removal fails too.
func RemoveAfterFailure(dir string, err error) error {
	if removeErr := os.RemoveAll(dir); removeErr != nil {
		return errors.Join(err, fmt.Errorf("remove temporary directory after failed setup: %w", removeErr))
	}
	return err
}
