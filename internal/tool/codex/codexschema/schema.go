// Package codexschema holds the Codex SQLite DDL and database file names that
// the codex package's test fixtures and the demo seeder both create.
package codexschema

// StateDBFileName, MemoriesDBFileName, and QueueDBFileName are the database
// file names Codex writes under its SQLite home (STATE_DB_FILENAME,
// MEMORIES_DB_FILENAME, and QUEUE_DB_FILENAME in codex-rs/state/src/sqlite.rs).
// Only fixture builders use them; the codex adapter globs by generation
// suffix and never pins a name.
const (
	StateDBFileName    = "state_5.sqlite"
	MemoriesDBFileName = "memories_1.sqlite"
	QueueDBFileName    = "queue_1.sqlite"
)

// StateDBSchema creates the state database tables the fixtures populate:
// the threads table of state/migrations/0001_threads.sql and the
// backfill_state table of 0008_backfill_state.sql, without their indexes or
// seed row, followed by stateDBProjectsSchema.
const StateDBSchema = `
CREATE TABLE threads (
	id TEXT PRIMARY KEY,
	rollout_path TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	source TEXT NOT NULL,
	model_provider TEXT NOT NULL,
	cwd TEXT NOT NULL,
	title TEXT NOT NULL,
	sandbox_policy TEXT NOT NULL,
	approval_mode TEXT NOT NULL,
	tokens_used INTEGER NOT NULL DEFAULT 0,
	has_user_event INTEGER NOT NULL DEFAULT 0,
	archived INTEGER NOT NULL DEFAULT 0,
	archived_at INTEGER,
	git_sha TEXT,
	git_branch TEXT,
	git_origin_url TEXT
);
CREATE TABLE backfill_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	status TEXT NOT NULL,
	last_watermark TEXT,
	last_success_at INTEGER,
	updated_at INTEGER NOT NULL
);` + stateDBProjectsSchema

// stateDBProjectsSchema is the table DDL of Codex's
// state/migrations/0049_projects.sql, verbatim. The migration's two indexes
// are omitted: idx_threads_project_id covers threads columns the fixture's
// reduced threads table does not declare, and idx_projects_position changes
// query plans only, never the rows cc-port reads or rewrites.
const stateDBProjectsSchema = `
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    position INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

CREATE TABLE project_roots (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    path TEXT NOT NULL,
    PRIMARY KEY (project_id, position)
);

CREATE TABLE project_idempotency_keys (
    key TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL
);

ALTER TABLE threads ADD COLUMN project_id TEXT
    REFERENCES projects(id) ON DELETE SET NULL;`

// MemoriesDBSchema is the stage1_outputs table of Codex's
// state/memory_migrations/0001_memories.sql, without its index or the jobs
// table.
const MemoriesDBSchema = `
CREATE TABLE stage1_outputs (
	thread_id TEXT PRIMARY KEY,
	source_updated_at INTEGER NOT NULL,
	raw_memory TEXT NOT NULL,
	rollout_summary TEXT NOT NULL,
	rollout_slug TEXT,
	generated_at INTEGER NOT NULL,
	usage_count INTEGER,
	last_usage INTEGER,
	selected_for_phase2 INTEGER NOT NULL DEFAULT 0,
	selected_for_phase2_source_updated_at INTEGER
);`

// QueueDBSchema is state/queue_migrations/0001_queued_items.sql followed by
// 0002_queued_thread_revisions.sql, verbatim.
const QueueDBSchema = `
CREATE TABLE queued_items (
    id TEXT PRIMARY KEY NOT NULL,
    thread_id TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    queue_order INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

CREATE UNIQUE INDEX queued_items_thread_order_idx
    ON queued_items(thread_id, queue_order);
CREATE TABLE queued_thread_revisions (
    revision INTEGER PRIMARY KEY AUTOINCREMENT,
    thread_id TEXT NOT NULL UNIQUE
);

INSERT INTO queued_thread_revisions (thread_id)
SELECT DISTINCT thread_id FROM queued_items ORDER BY thread_id;

CREATE TRIGGER queued_items_revision_after_insert
AFTER INSERT ON queued_items
BEGIN
    INSERT INTO queued_thread_revisions (thread_id)
    VALUES (NEW.thread_id)
    ON CONFLICT(thread_id) DO UPDATE
    SET revision = (SELECT COALESCE(MAX(revision), 0) + 1 FROM queued_thread_revisions);
END;

CREATE TRIGGER queued_items_revision_after_update
AFTER UPDATE ON queued_items
BEGIN
    INSERT INTO queued_thread_revisions (thread_id)
    VALUES (NEW.thread_id)
    ON CONFLICT(thread_id) DO UPDATE
    SET revision = (SELECT COALESCE(MAX(revision), 0) + 1 FROM queued_thread_revisions);
END;

CREATE TRIGGER queued_items_revision_after_delete
AFTER DELETE ON queued_items
BEGIN
    INSERT INTO queued_thread_revisions (thread_id)
    VALUES (OLD.thread_id)
    ON CONFLICT(thread_id) DO UPDATE
    SET revision = (SELECT COALESCE(MAX(revision), 0) + 1 FROM queued_thread_revisions);
END;`
