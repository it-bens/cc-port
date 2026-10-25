# internal/testmain

## Purpose

Helpers for a package's `TestMain`. A test binary sets up process-wide state, runs the tests, and tears the state down again; this package runs that sequence in one place so each `TestMain` states only its own setup and cleanup.

## Public API

- `Run(setup func() (cleanup func() error, err error), run func() int) int`: calls `setup`, then `run`, then the cleanup `setup` returned, and returns `run`'s exit code for `TestMain` to pass to `os.Exit`. A setup error prints to stderr and yields exit code 1 without running `run`. A cleanup error prints to stderr and yields exit code 1.
- `RemoveAfterFailure(dir string, err error) error`: removes `dir` after a failed setup step and returns `err`, joined with the removal error when the removal fails too.

## Tests

`testmain_test.go` injects the setup, run and cleanup functions: `Run` returns `run`'s exit code, returns 1 without running after a setup error, returns 1 after a cleanup error, and calls cleanup after `run`. The two error tests capture `os.Stderr` through a pipe and assert each error reaches it. `RemoveAfterFailure` returns the setup error and removes the directory.
