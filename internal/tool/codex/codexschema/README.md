# internal/tool/codex/codexschema

## Purpose

The single definition of Codex's SQLite DDL and on-disk layout names (database file names and memory worktree directory names) that cc-port creates outside Codex itself. When a Codex migration changes a table these databases declare, edit it here once.

## Public API

- `StateDBFileName`, `MemoriesDBFileName`, `QueueDBFileName`: `state_5.sqlite`, `memories_1.sqlite`, `queue_1.sqlite`, the names in `codex-rs/state/src/sqlite.rs`. Only fixture builders pin them; the codex adapter globs (see `internal/tool/codex/README.md` §Glob, don't pin).
- `StateDBSchema`: the `threads` table from `state/migrations/0001_threads.sql`, the `backfill_state` table from `0008_backfill_state.sql`, and the `0049_projects.sql` tables plus its `threads.project_id` column. Indexes and seed rows are left out.
- `MemoriesDBSchema`: the `stage1_outputs` table from `state/memory_migrations/0001_memories.sql`.
- `QueueDBSchema`: `state/queue_migrations/0001_queued_items.sql` and `0002_queued_thread_revisions.sql`, verbatim.
- `MemoriesWorktreeSubdir`, `MemoriesWorktreeV2Subdir`: `memories` and `memories_v2`, the names `MemoryVersion::directory_name()` returns in `protocol/src/memory_version.rs`.
- `GitDirName`: `.git`, the git baseline directory under each memory worktree root.

## Tests

No tests of its own. Package codex's tests run every constant through `codextest.SetupFixture`.
