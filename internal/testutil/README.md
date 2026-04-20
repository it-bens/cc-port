# internal/testutil

## Purpose

Fixture helpers for unit tests. `SetupFixture` stages a realistic `~/.claude` tree under `t.TempDir()` from the repo-root `testdata/dotclaude/` fixture and returns a `*claude.Home` rooted at the staged copy.

## Public API

- `SetupFixture(t *testing.T) *claude.Home`: stages `testdata/dotclaude/` under `t.TempDir()`, extracts `.claude.json` to a sibling path, and rewrites every `sessions/*.json` PID to a dead sentinel. Returns `*claude.Home{Dir: .../dotclaude, ConfigFile: .../dotclaude.json}`. Pass the returned home into `move.DryRun`, `importer.Run`, etc.

## Fixture shape

`testdata/` is found by walking upward from `os.Getwd()`. Tests run from arbitrary package directories, so the helper ascends until it finds a `testdata/` sibling or reaches the filesystem root. A test invoked outside the repository tree fails immediately.

Session PIDs are rewritten to a dead sentinel before the home is returned. `internal/lock`'s live-session check calls `Kill(pid, 0)` on each `sessions/*.json` PID. A fixture PID matching a live process would be flagged as an active Claude Code session and refuse the lock.

`.claude.json` is moved out of the copied `~/.claude/` tree to `<tempdir>/dotclaude.json`. Callers see both `Home.Dir` and `Home.ConfigFile` without the config file shadowing real on-disk content inside `~/.claude/`.

## Tests

`fixture_test.go`: smoke test that `SetupFixture` returns a usable home whose project directory exists, whose `history.jsonl` is present, and whose `.claude.json` parses as JSON.
