## Named-value assignments

- `project.stacks` = `go` (single stack; `go.mod`, Go 1.26)
- `tests.frameworks` = stdlib `testing` plus testify — `require` for preconditions, `assert` for behavioral claims after the act; table-driven via `t.Run`. Runners: `make test` (unit), `make test-integration` (`-tags integration`, gates the root `integration_test.go` suite), `make test-large` (`-tags large`).
- `tests.fixture_sources` =
  - `testutil.SetupFixture(t)` — staged `~/.claude` tree from the repo `testdata/dotclaude/` tree.
  - `codex.SetupFixture(t)` — staged `~/.codex` tree from `internal/tool/codex/testdata/dotcodex/`; the SQLite databases and the `memories/.git` baseline are built at runtime by the helper — never add on-disk copies to the fixture tree.
  - `testutil.WriteFixtureArchive(t)` — a known-good export archive.
  - `testutil.FixtureProjectPath()` / `codex.FixtureProjectPath()` — the canonical project key the fixture trees are staged around.
  - Extend these helpers before inventing a new one.
- `tests.parallelism` = No test calls `t.Parallel()`. `TestMain` exists only in `cmd/cc-port/main_test.go` and the root `integration_test.go`, where it runs `testutil.IsolateHome()` so command tests never observe the live `~/.claude` or `~/.codex`; fixture homes still travel by explicit flags. Guard independence as if order were randomized and concurrent anyway.
- `tests.scale_gating` = `//go:build large` sibling files for production-scale cap fixtures (currently `internal/importer/importer_large_test.go`). Pair every large-gated test with an untagged small-cap variant exercising the same branches at KiB scale by passing a small `archive.Caps{...}` straight into the call under test — caps are injected parameters, never package state. CI runs the tagged suite in a dedicated step (`go test -tags large ./internal/importer/...`); `make test-large` covers it locally. Document any branch only the large variant reaches in that test's leading comment.

## Post-Step-2

When a behavior hides behind the current exported API, match against the seams production already carries: `io.Writer` parameters wired by cobra's `cmd.OutOrStdout()`; constructor-field injection (`codex.NewAdapter(getenv, listProcesses)`, `claude.NewWorkspaceForTest` with a caller-supplied process-liveness check); exported pure helpers (`claude.RewriteSessionFile`, `claude.RewriteUserConfig`); package-level fn-vars swapped under `t.Cleanup`. Write codex-mutation tests at the importer/adapter level through `codex.NewWorkspace`, never at cmd level — the test suite itself runs as a codex process, so a cmd-level mutation test refuses on the live-writer witness and proves nothing.

## Post-Step-3

Use the project's descriptive-identifier shapes: project paths as `/Users/test/Projects/<name>`, session IDs as `"primary-session"` / `"orphaned-session"`, file-history keys as `"edited-file"`, manifest entries as `"first-snapshot"`.
