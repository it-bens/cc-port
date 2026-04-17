# internal/testutil

## Purpose

Fixture helpers shared across unit tests. Stages a realistic `~/.claude` tree under `t.TempDir()` from the bundled `testdata/dotclaude/` fixture and returns a `*claude.Home` rooted at the staging directory.

## Public API

- `SetupFixture(t *testing.T) *claude.Home` — copy `testdata/dotclaude/` to a temp dir, register cleanup, and return a `*claude.Home` configured to use it. Callers pass the returned home into `move.DryRun`, `importer.Run`, etc.

## Tests

`fixture_test.go` — smoke test that `SetupFixture` returns a usable home whose project directory exists and whose `.claude.json` parses.
