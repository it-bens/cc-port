# internal/testmain

## Purpose

Helpers for a package's `TestMain`. A test binary sets up process-wide state, runs the tests, and tears the state down again; this package runs that sequence in one place so each `TestMain` states only its own setup and cleanup.

## Public API

- `Run(setup func() (cleanup func() error, err error), run func() int) int`: calls `setup`, then `run`, then the cleanup `setup` returned, and returns `run`'s exit code for `TestMain` to pass to `os.Exit`. A setup error prints to stderr and yields exit code 1 without running `run`. A cleanup error prints to stderr and yields exit code 1.
- `StageTempDir(pattern string, stage func(dir string) error) (cleanup func() error, err error)`: creates a temporary directory whose name includes `pattern`, calls `stage` with it, and returns a cleanup function that removes the directory. If `stage` fails, it removes the directory and returns a nil cleanup and the stage error, joined with the removal error when the removal fails too.
- `RemoveAfterFailure(dir string, err error) error`: removes `dir` after a failed setup step and returns `err`, joined with the removal error when the removal fails too.

## Tests

`testmain_test.go` injects the setup, run and cleanup functions: `Run` returns `run`'s exit code, returns 1 without running after a setup error, returns 1 after a cleanup error, and calls cleanup after `run`. The two error tests capture `os.Stderr` through a pipe and assert each error reaches it. `StageTempDir` passes `stage` an existing directory, and its cleanup removes that directory. If `stage` fails, `StageTempDir` returns its error and removes the directory. A creation failure returns an error wrapping `fs.ErrNotExist` without calling `stage`. A removal failure returns an error wrapping `fs.ErrPermission`. `RemoveAfterFailure` returns the setup error and removes the directory. If removal fails, it returns the setup error joined with the removal error.
