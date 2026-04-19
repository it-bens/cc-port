# internal/testutil

## Purpose

Fixture helpers shared across unit tests. Stages a realistic `~/.claude` tree under `t.TempDir()` from the repo-root `testdata/dotclaude/` fixture (resolved by walking up from `os.Getwd()`) and returns a `*claude.Home` rooted at the staging directory.

## Public API

- `SetupFixture(t *testing.T) *claude.Home` — copy `testdata/dotclaude/` into `t.TempDir()`, extract `.claude.json` from the copied tree to a sibling path `dotclaude.json` (removing the original), and return `*claude.Home{Dir: …/dotclaude, ConfigFile: …/dotclaude.json}`. Cleanup is registered via `t.TempDir()`. Callers pass the returned home into `move.DryRun`, `importer.Run`, etc.

## Tests

`fixture_test.go` — smoke test that `SetupFixture` returns a usable home whose project directory exists and whose `.claude.json` parses.
