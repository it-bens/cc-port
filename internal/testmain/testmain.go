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

// RemoveAfterFailure removes dir after a failed setup step and returns err,
// joined with the removal error when the removal fails too.
func RemoveAfterFailure(dir string, err error) error {
	if removeErr := os.RemoveAll(dir); removeErr != nil {
		return errors.Join(err, fmt.Errorf("remove temporary directory after failed setup: %w", removeErr))
	}
	return err
}
