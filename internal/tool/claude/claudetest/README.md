# internal/tool/claude/claudetest

## Purpose

Fixture helpers for unit tests. `SetupFixture` stages a realistic `~/.claude` tree under `t.TempDir()` from the repo-root `testdata/dotclaude/` fixture and returns a `*claude.Home` rooted at the staged copy.

## Public API

- `SetupFixture(t *testing.T) *claude.Home`: stages `testdata/dotclaude/` under `t.TempDir()`, extracts `.claude.json` to a sibling path, and rewrites every `sessions/*.json` PID to a dead sentinel. Returns `*claude.Home{Dir: .../dotclaude, ConfigFile: .../dotclaude.json}`. Pass the returned home into `move.DryRun`, `importer.Run`, etc.
- `StageFixture(dir string) (*claude.Home, error)`: stages the fixture under `dir` and returns a `*claude.Home` pointing at the staged copy. It takes no `testing.TB`, so a package can stage one fixture before running any test.
- `RunWithSharedFixture(run func() int) int`: installs the shared fixture, runs `run`, and removes the fixture, returning the exit code for `TestMain` to pass to `os.Exit`. It runs that setup, run and cleanup sequence through `testmain.Run`; the order and the exit code rule live in `internal/testmain/README.md §Public API`.
- `SharedFixture(t testing.TB) *claude.Home`: returns the home `RunWithSharedFixture` staged. Only tests that never write under the home may use it; a test that writes stages its own copy with `SetupFixture`. A package that did not install one fails the test with a message naming `RunWithSharedFixture`.
- `NewWorkspace(home *claude.Home) *claude.Workspace`: binds a workspace to `home` with the adapter's default environment, process-liveness and process-start-time seams. Tests that supply their own seams call `claude.NewWorkspace(home, getenv, processLiveness, processStartTime)`.
- `RunWithIsolatedHome(run func() int) int`: points `HOME` at a new temp directory holding only an empty `.claude` marker, so Codex is never detected, runs `run`, then restores `HOME` and removes the directory. Called from `TestMain`, directly or inside `codextest.RunWithTemplate`.
- `FixtureProjectPath() string`: returns `/Users/test/Projects/myproject`, the project path the fixture tree is keyed on.

## Fixture shape

`testdata/` is found by walking upward from `os.Getwd()`. Tests run from arbitrary package directories, so the helper ascends until it finds a `testdata/` directory holding `dotclaude/` or reaches the filesystem root. A test invoked outside the repository tree fails immediately.

Session PIDs are rewritten to a dead sentinel before the home is returned. The Claude adapter's live-session check, `claude.ProcessAlive`, sends signal 0 to each `sessions/*.json` PID. A fixture PID matching a live process would be flagged as an active Claude Code session and refuse the lock.

`.claude.json` is moved out of the copied `~/.claude/` tree to `<tempdir>/dotclaude.json`. Callers see both `Home.Dir` and `Home.ConfigFile` without the config file shadowing real on-disk content inside `~/.claude/`.

## Tests

`fixture_test.go`: smoke test that `SetupFixture` returns a usable home whose project directory exists, whose `history.jsonl` is present, and whose `.claude.json` parses as JSON; and three tests of `RunWithIsolatedHome` that it points `HOME` at an isolated home holding `.claude`, removes that directory afterwards, and leaves an unset `HOME` unset.
