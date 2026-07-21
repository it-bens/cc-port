# internal/tool/codex: agent notes

## Before editing

- Glob every database discovery (`state_*.sqlite`, `memories_*.sqlite`, ...); never pin a generation-suffixed filename. (README §Glob, don't pin)
- Mutate SQLite databases only through `internal/sqlrewrite`; a read-only `BEGIN IMMEDIATE` lock probe may open a write connection and must roll back without row mutation. (README §Witness evidence order)
- Never `INSERT` into the state database; it is a foreign, self-healing cache Codex's own reconciler owns. (README §Sidecar update-only rationale)
- Walk both rollout roots (`sessions/`, `archived_sessions/`) in every rollout surface; never assume one. (README §Both-roots coverage)
- Preserve rollout filenames exactly; `rewriteRolloutLines` rewrites content in place at the same path, never renames. (README §Era-A rollout handling)
- Append to `history.jsonl` and `session_index.jsonl` with `O_APPEND`, never rename-replace; a replace would invalidate `history.jsonl`'s inode-keyed TUI cache and could drop a concurrent Codex append to either file. (README §History and session-index append-only)
- Never export or import `config.toml`; trust is a per-machine decision. (README §Config never ported)
- Compute a project's stats/export thread-ID set only through `projectThreadIDSet`; never re-derive it from rollouts alone. (README §Reference thread-ID union)

## Navigation

- Home and SQLite-dir resolution: `home.go`, `databases.go`.
- Move surfaces: `move.go`, `databaseapply.go`, `statedb.go`, `memories.go`, `toml.go`, `agents.go`.
- Witness: `witness.go`, `process.go`.
- Rollout JSONL: `rollout.go`.
- Export/import/stats and the threads sidecar: `export_import_stats.go`.
- Test fixtures: `fixture.go`.
