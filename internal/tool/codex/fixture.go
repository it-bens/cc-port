package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	// Registers the "sqlite" database/sql driver used below.
	_ "modernc.org/sqlite"

	"github.com/it-bens/cc-port/internal/fsutil"
)

// FixtureProjectPath returns the canonical project path the fixture tree
// is keyed on: config.toml's [projects] table, every rollout's
// session_meta/turn_context cwd, and the fixture SQLite rows SetupFixture
// builds all reference it.
func FixtureProjectPath() string {
	return "/Users/fixture/codexproject"
}

// SetupFixture stages testdata/dotcodex under t.TempDir() and builds
// fixture state_5.sqlite and memories_1.sqlite databases, plus a no-remote
// .git baseline under each memory worktree root (memories/ and
// memories_v2/), alongside it — SQLite files are binary and nested .git
// directories are untrackable by the outer repo, so all of it is built by
// test code rather than committed, following the testutil.SetupFixture
// pattern. sqlite_home resolves to the same directory (config.toml
// declares no sqlite_home key and $CODEX_SQLITE_HOME is unset in the
// fixture environment).
func SetupFixture(t *testing.T) *Home {
	t.Helper()

	codexDir := filepath.Join(t.TempDir(), "dotcodex")
	fixtureDir := findFixtureDir(t)
	if err := fsutil.CopyDir(context.Background(), filepath.Join(fixtureDir, "dotcodex"), codexDir, nil); err != nil {
		t.Fatalf("copy fixture directory: %v", err)
	}

	buildFixtureStateDB(t, filepath.Join(codexDir, stateDBFileName))
	buildFixtureMemoriesDB(t, filepath.Join(codexDir, memoriesDBFileName))
	buildFixtureMemoriesGitBaseline(t, filepath.Join(codexDir, memoriesWorktreeSubdir), fixtureGitConfigNoRemote)
	buildFixtureMemoriesV2Worktree(t, filepath.Join(codexDir, memoriesWorktreeV2Subdir))

	return &Home{Dir: codexDir, SQLiteDir: codexDir}
}

// InsertThreadRow inserts a threads row with id and cwd directly into
// home's fixture state database, for tests exercising a project
// SetupFixture's canned fixture does not cover, such as a symlink-aliased
// cwd built under t.TempDir() by a cross-package end-to-end move test.
func InsertThreadRow(t *testing.T, home *Home, id, cwd string) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, stateDBFileName))
	if err != nil {
		t.Fatalf("open fixture state db: %v", err)
	}
	defer func() { _ = database.Close() }()

	now := time.Now().Unix()
	_, err = database.ExecContext(context.Background(),
		`INSERT INTO threads
			(id, rollout_path, created_at, updated_at, source, model_provider, cwd, title, sandbox_policy, approval_mode)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "archived_sessions/rollout-"+id+".jsonl", now, now, "cli", "openai", cwd, "fixture thread", "workspace-write", "on-request",
	)
	if err != nil {
		t.Fatalf("insert fixture thread row for %s: %v", cwd, err)
	}
}

// ThreadCWD reads back threads.cwd for id from home's fixture state
// database, for tests asserting a move rewrote it.
func ThreadCWD(t *testing.T, home *Home, id string) string {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, stateDBFileName))
	if err != nil {
		t.Fatalf("open fixture state db: %v", err)
	}
	defer func() { _ = database.Close() }()

	var cwd string
	if err := database.QueryRowContext(context.Background(), `SELECT cwd FROM threads WHERE id = ?`, id).Scan(&cwd); err != nil {
		t.Fatalf("read threads.cwd for %s: %v", id, err)
	}
	return cwd
}

// FixtureAgentsDir stages a minimal ~/.agents/plugins/marketplace.json
// fixture under t.TempDir() and returns the ~/.agents path, for tests
// exercising the optional agents-marketplace surface.
func FixtureAgentsDir(t *testing.T) string {
	t.Helper()
	agentsDir := filepath.Join(t.TempDir(), "dotagents")
	pluginsDir := filepath.Join(agentsDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o750); err != nil {
		t.Fatalf("create fixture agents plugins dir: %v", err)
	}
	marketplace := `{"entries":[{"name":"local-fixture","source":"` +
		FixtureProjectPath() + `/.agents-marketplace"}]}`
	if err := os.WriteFile(filepath.Join(pluginsDir, "marketplace.json"), []byte(marketplace), 0o600); err != nil {
		t.Fatalf("write fixture marketplace.json: %v", err)
	}
	return agentsDir
}

func findFixtureDir(t *testing.T) string {
	t.Helper()

	currentDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	for {
		candidate := filepath.Join(currentDir, "internal", "tool", "codex", "testdata")
		if info, statErr := os.Stat(filepath.Join(candidate, "dotcodex")); statErr == nil && info.IsDir() {
			return candidate
		}
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			t.Fatal("could not find testdata/dotcodex/ directory")
		}
		currentDir = parentDir
	}
}

// stateDBFileName and memoriesDBFileName are the generation-suffixed
// filenames SetupFixture writes; production code never pins these and
// always globs (databases.go).
const (
	stateDBFileName    = "state_5.sqlite"
	memoriesDBFileName = "memories_1.sqlite"
)

// fixtureGitConfigNoRemote is a local-only baseline config, the shape
// hasNoRemoteGitBaseline accepts.
const fixtureGitConfigNoRemote = "[core]\n\trepositoryformatversion = 0\n"

// fixtureGitConfigWithRemote attaches the baseline to a remote, the shape a
// move leaves in place.
const fixtureGitConfigWithRemote = fixtureGitConfigNoRemote +
	"[remote \"origin\"]\n\turl = https://example.invalid/repo.git\n"

// buildFixtureMemoriesGitBaseline creates a .git baseline under root whose
// config is config, at runtime: git never tracks a nested .git directory, so
// — like the SQLite fixtures below — test code builds it instead of
// committing it. Calling it on a root that already has a baseline replaces
// that baseline's config.
func buildFixtureMemoriesGitBaseline(t *testing.T, root, config string) {
	t.Helper()
	gitDir := filepath.Join(root, gitDirName)
	if err := os.MkdirAll(gitDir, 0o750); err != nil {
		t.Fatalf("create fixture memories git baseline: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("write fixture memories git baseline config: %v", err)
	}
}

// buildFixtureMemoriesV2Worktree stages a memories_v2/ worktree mirroring
// Codex's real v2 layout, plus its own no-remote .git baseline, entirely at
// runtime: like memories/'s SQLite and git fixtures above, nothing under
// memories_v2/ is committed to testdata/dotcodex/. Unlike memories/, v2
// never gets raw_memories.md: sync_phase2_workspace_inputs
// (memories/write/src/phase2.rs:196-206) calls
// rebuild_raw_memories_file_from_memories only for MemoryVersion::V1,
// while sync_rollout_summaries_from_memories runs for both versions, so
// rollout_summaries/*.md is the one deterministic file family both
// versions share. memory_summary.md is a consolidation-agent-authored
// artifact validate_consolidation_artifacts_for_version reads for both
// versions (memories/write/src/workspace.rs:101-114); it is staged here too
// since cc-port's worktree rewrite walks every file under root, not a fixed
// filename set, and a real v2 worktree carries it.
func buildFixtureMemoriesV2Worktree(t *testing.T, root string) {
	t.Helper()
	summariesDir := filepath.Join(root, "rollout_summaries")
	if err := os.MkdirAll(summariesDir, 0o750); err != nil {
		t.Fatalf("create fixture memories_v2 worktree: %v", err)
	}

	summary := "thread_id: 00000000-0000-4000-8000-000000000001\n" +
		"cwd: " + FixtureProjectPath() + "\n\n" +
		"Summary: fixed a bug in " + FixtureProjectPath() + "/src/main.py.\n"
	if err := os.WriteFile(filepath.Join(summariesDir, "2026-07-17T10-00-00-a1b2.md"), []byte(summary), 0o600); err != nil {
		t.Fatalf("write fixture memories_v2 rollout summary: %v", err)
	}

	// Headings match is_valid_v2_summary's required set
	// (memories/write/src/workspace.rs:122-125), so this fixture is a valid
	// v2 memory_summary.md, not merely a plausible-looking one.
	memorySummary := "v1\n\n" +
		"## User Profile\n\n" +
		"## User preferences\n\n" +
		"## General Tips\n\n" +
		"## What's in Memory\n\n" +
		"Fixed a bug in " + FixtureProjectPath() + "/src/main.py.\n"
	if err := os.WriteFile(filepath.Join(root, "memory_summary.md"), []byte(memorySummary), 0o600); err != nil {
		t.Fatalf("write fixture memories_v2 memory_summary.md: %v", err)
	}

	buildFixtureMemoriesGitBaseline(t, root, fixtureGitConfigNoRemote)
}

// fixtureProjectID is the projects.id of the fixture's one Codex project,
// whose single project_roots row holds FixtureProjectPath.
const fixtureProjectID = "primary-project"

// stateDBProjectsSchema is the table DDL of Codex's
// state/migrations/0049_projects.sql, verbatim. The migration's two indexes
// are omitted: idx_threads_project_id covers threads columns the fixture's
// reduced threads table does not declare.
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

func buildFixtureStateDB(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture state db: %v", err)
	}
	defer func() { _ = database.Close() }()

	const schema = `
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
	if _, err := database.ExecContext(context.Background(), schema); err != nil {
		t.Fatalf("create fixture state schema: %v", err)
	}

	now := time.Now().Unix()
	_, err = database.ExecContext(context.Background(),
		`INSERT INTO threads
			(id, rollout_path, created_at, updated_at, source, model_provider, cwd, title, sandbox_policy, approval_mode)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"00000000-0000-4000-8000-000000000001",
		"sessions/2026/07/17/rollout-2026-07-17T10-00-00-00000000-0000-4000-8000-000000000001.jsonl",
		now, now, "cli", "openai", FixtureProjectPath(), "fix login bug", "workspace-write", "on-request",
	)
	if err != nil {
		t.Fatalf("insert fixture thread: %v", err)
	}

	_, err = database.ExecContext(context.Background(),
		`INSERT INTO projects (id, name, metadata, position, created_at_ms, updated_at_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		fixtureProjectID, "codexproject", "{}", 0, now*1000, now*1000,
	)
	if err != nil {
		t.Fatalf("insert fixture project: %v", err)
	}
	_, err = database.ExecContext(context.Background(),
		`INSERT INTO project_roots (project_id, position, path) VALUES (?, ?, ?)`,
		fixtureProjectID, 0, FixtureProjectPath(),
	)
	if err != nil {
		t.Fatalf("insert fixture project root: %v", err)
	}

	const backfillEpoch = 1_752_137_200
	_, err = database.ExecContext(context.Background(),
		`INSERT INTO backfill_state (id, status, last_watermark, last_success_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`,
		1, "complete", "sessions/2026/07/17/rollout-2026-07-17T10-00-00-00000000-0000-4000-8000-000000000001.jsonl", backfillEpoch, backfillEpoch,
	)
	if err != nil {
		t.Fatalf("insert fixture backfill state: %v", err)
	}
}

func buildFixtureMemoriesDB(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open fixture memories db: %v", err)
	}
	defer func() { _ = database.Close() }()

	const schema = `
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
	if _, err := database.ExecContext(context.Background(), schema); err != nil {
		t.Fatalf("create fixture memories schema: %v", err)
	}

	now := time.Now().Unix()
	_, err = database.ExecContext(context.Background(),
		`INSERT INTO stage1_outputs (thread_id, source_updated_at, raw_memory, rollout_summary, rollout_slug, generated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
		"00000000-0000-4000-8000-000000000001", now,
		"Fixed a bug in "+FixtureProjectPath()+"/src/main.py.",
		"Summary: fixed a bug in "+FixtureProjectPath()+"/src/main.py.",
		"fix-login-bug", now,
	)
	if err != nil {
		t.Fatalf("insert fixture stage1 output: %v", err)
	}
}
