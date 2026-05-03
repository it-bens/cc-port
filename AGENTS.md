# cc-port: agent notes

Go CLI that rewrites Claude Code project state. See `README.md` for the project overview.

## Before editing anywhere

- Never inspect or rewrite file-history snapshot contents. (docs/architecture.md §File-history policy (cross-cutting))
- Add any new session-keyed directory by appending one entry to both `claude.SessionKeyedGroups` and `transport.SessionKeyedTargets`. (internal/claude/README.md §Session-keyed registry)
- Register every export category in `manifest.AllCategories`. Never hard-code a parallel category list. (internal/manifest/README.md §Category manifest)
- Route every path-substring rewrite through `rewrite.ReplacePathInBytes`. Never call `strings.ReplaceAll` on user paths. (internal/rewrite/README.md §Boundary rules)
- Wrap every mutating command body (`move --apply`, `import`) in `lock.WithLock` before any write. (internal/lock/README.md §Concurrency guard)
- Contain adversarial archive paths with `os.Root` and bound decompressed reads with `io.LimitReader`. (internal/importer/README.md §Import contract)
- After editing archive cap-guard code, run `go test -tags large ./internal/importer/...` locally. (internal/importer/README.md §Tests)
- Set an explicit `bufio.Scanner.Buffer` cap on any new line-scanned read over untrusted input. (internal/scan/README.md §Rules files preserved)
- Cap any `bufio.Scanner` reader of `history.jsonl` with `claude.MaxHistoryLine`. (internal/claude/README.md §History line cap)

## Navigation

- CLI entry: `cmd/cc-port`.
- Commands: `internal/move`, `internal/export`, `internal/importer`, `internal/sync`.
- Shared primitives: `internal/rewrite`, `internal/lock`, `internal/fsutil`, `internal/claude`, `internal/scan`, `internal/ui`, `internal/pipeline`, `internal/file`.
- Modules with hard editing rules additionally carry an `AGENTS.md`.
