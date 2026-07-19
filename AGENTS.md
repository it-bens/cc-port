# cc-port: agent notes

Go CLI that ports Claude Code and OpenAI Codex project state. See `README.md` for the project overview.

## Before editing anywhere

- Never inspect or rewrite file-history snapshot contents. (docs/architecture.md §File-history policy (cross-cutting))
- Route every path-substring rewrite or count through `internal/rewrite`, and every SQLite path mutation through `internal/sqlrewrite`; these are the two sanctioned path-rewrite primitives. Never call `strings.ReplaceAll` or `strings.Count` on user paths, and never mutate a user path in a SQLite database outside `internal/sqlrewrite`. (internal/rewrite/README.md §Boundary rules, internal/sqlrewrite/README.md §Contracts)
- Command packages (`internal/move`, `internal/export`, `internal/importer`, `internal/stats`, `internal/sync`) import `internal/tool` only; only `cmd/cc-port` imports an adapter package. (docs/architecture.md §The tool contract)
- Add any new session-keyed directory as one row in `claude.Registries`. (internal/tool/claude/README.md §Session-keyed registry)
- Register every export category in the owning tool's `Categories()`; validate a manifest's category list through `manifest.ApplyToolCategories`. Never hard-code a parallel category list. (internal/manifest/README.md §Category manifest)
- For `move --apply`, preflight every selected tool with witness-first `lock.Acquire` in registry order, hold all flocks through apply, and release in reverse order; wrap `import` in nested `lock.WithLock` across selected tools. (internal/lock/README.md §Concurrency guard)
- Contain adversarial archive paths with `os.Root` and bound decompressed reads with per-entry and aggregate caps. (internal/archive/README.md §Contracts)
- After editing archive or zstd cap-guard code, run `go test -tags large ./internal/importer/... ./internal/tool/codex/...` locally. (internal/importer/README.md §Tests, internal/tool/codex/README.md §Tests)
- Set an explicit `bufio.Scanner.Buffer` cap on any new line-scanned read over untrusted input. (internal/scan/README.md §Rules files preserved)
- Cap any `bufio.Scanner` reader of Claude's `history.jsonl` with `claude.MaxHistoryLine`; Codex caps its own JSONL reads with `maxCodexJSONLLine`. (internal/tool/claude/README.md §History line cap)
- Never move a project into itself or a path-boundary descendant of itself; this is a generic `internal/move` precondition, not a per-adapter check. (internal/move/README.md §Refused)

## Navigation

- CLI entry: `cmd/cc-port`, which also owns the tool registry (`cmd/cc-port/tools.go`).
- Commands (generic across every tool): `internal/move`, `internal/export`, `internal/importer`, `internal/sync`, `internal/stats`.
- Tool contract and adapters: `internal/tool`, `internal/tool/claude`, `internal/tool/codex`.
- Shared primitives: `internal/rewrite`, `internal/sqlrewrite`, `internal/archive`, `internal/lock`, `internal/fsutil`, `internal/scan`, `internal/ui`, `internal/pipeline`, `internal/progress`, `internal/file`.
- Modules with hard editing rules additionally carry an `AGENTS.md`.
