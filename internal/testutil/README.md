# internal/testutil

## Purpose

Fixture helpers shared across unit tests. Stages a realistic `~/.claude` tree under `t.TempDir()` from the repo-root `testdata/dotclaude/` fixture (resolved by walking up from `os.Getwd()`) and returns a `*claude.Home` rooted at the staging directory.

## Public API

- `SetupFixture(t *testing.T) *claude.Home` — copy `testdata/dotclaude/` into `t.TempDir()`, extract `.claude.json` from the copied tree to a sibling path `dotclaude.json` (removing the original), rewrite every `sessions/*.json` PID to a sentinel dead value, and return `*claude.Home{Dir: …/dotclaude, ConfigFile: …/dotclaude.json}`. Cleanup is registered via `t.TempDir()`. Callers pass the returned home into `move.DryRun`, `importer.Run`, etc.

## Contracts

### Fixture shape

- **`.claude.json` extracted to a sibling path.** The file is moved out of the copied `~/.claude/` tree to `<tempdir>/dotclaude.json`; the original is removed. Callers see both via the returned `Home.Dir` and `Home.ConfigFile` without the config file shadowing real on-disk content inside `~/.claude/`.
- **Session PIDs rewritten to a dead sentinel.** Every `sessions/*.json` has its `pid` field rewritten to `deadSessionPID` before the home is returned. `internal/lock`'s live-session check iterates these files and calls `Kill(pid, 0)`; a fixture PID that happened to match a live process on the test host would otherwise be flagged as a live Claude Code session and refuse the lock.
- **`testdata/` is located by walking upward from `os.Getwd()`.** Tests run from arbitrary package directories, so the helper ascends from the current directory until it finds a `testdata/` sibling or reaches the filesystem root. A test invoked outside the repository tree fails the test, not silently.

## Quirks

### Sentinel dead PID

`deadSessionPID` is `2_000_000_001` — larger than the default PID space on macOS (99998) and Linux (4194304), comfortably below `int32` max so `Kill(pid, 0)` reports `ESRCH` rather than an out-of-range error, and unlikely to collide with a real process on any plausible test runner.

## Tests

`fixture_test.go` — smoke test that `SetupFixture` returns a usable home whose project directory exists, whose `history.jsonl` is present, and whose `.claude.json` parses as JSON.
