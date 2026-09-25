# internal/tool/codex/codexschema

## Purpose

The one copy of the Codex SQLite DDL and database file names that cc-port creates outside Codex itself. Package codex's test fixtures (`SetupFixture` in `fixture.go`) and the demo seeder (`docs/videos/fixtures/cmd/seed-home`) both build their databases from it. When a Codex migration changes a table these databases declare, edit it here once.

The package imports neither `testing` nor package codex, so the seeder binary can link it.

## Public API

- `StateDBFileName`, `MemoriesDBFileName`, `QueueDBFileName`: `state_5.sqlite`, `memories_1.sqlite`, `queue_1.sqlite`, the names in `codex-rs/state/src/sqlite.rs`. Only fixture builders pin them; the codex adapter globs (see `internal/tool/codex/README.md` §Glob, don't pin).
- `StateDBSchema`: the `threads` table from `state/migrations/0001_threads.sql`, the `backfill_state` table from `0008_backfill_state.sql`, and the `0049_projects.sql` tables plus its `threads.project_id` column. Indexes and seed rows are left out.
- `MemoriesDBSchema`: the `stage1_outputs` table from `state/memory_migrations/0001_memories.sql`.
- `QueueDBSchema`: `state/queue_migrations/0001_queued_items.sql` and `0002_queued_thread_revisions.sql`, verbatim.

## Tests

No tests of its own. Package codex's tests run every constant through `SetupFixture`.
