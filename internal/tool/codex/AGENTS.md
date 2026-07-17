# internal/tool/codex: agent notes

## Before editing

- Glob every database discovery (`state_*.sqlite`, `memories_*.sqlite`, ...); never pin a generation-suffixed filename. (README §Glob, don't pin)
- Mutate any SQLite database only through `internal/sqlrewrite`; never open a write connection directly. (README §Sidecar update-only rationale, `internal/sqlrewrite/README.md`)
- Never `INSERT` into the state database; it is a foreign, self-healing cache Codex's own reconciler owns. (README §Sidecar update-only rationale)
- Walk both rollout roots (`sessions/`, `archived_sessions/`) in every rollout surface; never assume one. (README §Both-roots coverage)
- Preserve rollout filenames exactly; `TranscodeLines` rewrites content in place at the same path, never renames. (README §Era-A rollout handling)
- Append to `history.jsonl` and `session_index.jsonl` with `O_APPEND`, never rename-replace; the desktop TUI caches by inode. (README §Sidecar update-only rationale)
- Never export or import `config.toml`; trust is a per-machine decision. (README §Config never ported)

## Navigation

- Home and SQLite-dir resolution: `home.go`, `databases.go`.
- Move surfaces: `move.go`, `databaseapply.go`, `statedb.go`, `memories.go`, `toml.go`, `agents.go`.
- Witness: `witness.go`, `process.go`.
- Rollout JSONL and zstd transcoding: `rollout.go`, `transcode.go`.
- Export/import/stats and the threads sidecar: `export_import_stats.go`.
- Test fixtures: `fixture.go`.
