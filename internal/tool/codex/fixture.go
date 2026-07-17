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
// fixture state_5.sqlite and memories_1.sqlite databases, plus the
// memories/.git no-remote baseline, alongside it — SQLite files are
// binary and nested .git directories are untrackable by the outer repo,
// so both are built by test code rather than committed, following the
// testutil.SetupFixture pattern. sqlite_home resolves to the same
// directory (config.toml declares no sqlite_home key and
// $CODEX_SQLITE_HOME is unset in the fixture environment).
func SetupFixture(t *testing.T) *Home {
	t.Helper()

	codexDir := filepath.Join(t.TempDir(), "dotcodex")
	fixtureDir := findFixtureDir(t)
	if err := fsutil.CopyDir(context.Background(), filepath.Join(fixtureDir, "dotcodex"), codexDir, nil); err != nil {
		t.Fatalf("copy fixture directory: %v", err)
	}

	buildFixtureStateDB(t, filepath.Join(codexDir, stateDBFileName))
	buildFixtureMemoriesDB(t, filepath.Join(codexDir, memoriesDBFileName))
	buildFixtureMemoriesGitBaseline(t, filepath.Join(codexDir, memoriesWorktreeSubdir))

	return &Home{Dir: codexDir, SQLiteDir: codexDir}
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
		candidate := filepath.Join(currentDir, "testdata")
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

// buildFixtureMemoriesGitBaseline creates a no-remote memories/.git
// baseline at runtime: git never tracks a nested .git directory, so —
// like the SQLite fixtures below — test code builds it instead of
// committing it.
func buildFixtureMemoriesGitBaseline(t *testing.T, memoriesDir string) {
	t.Helper()
	gitDir := filepath.Join(memoriesDir, gitDirName)
	if err := os.MkdirAll(gitDir, 0o750); err != nil {
		t.Fatalf("create fixture memories git baseline: %v", err)
	}
	const config = "[core]\n\trepositoryformatversion = 0\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("write fixture memories git baseline config: %v", err)
	}
}

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
CREATE TABLE agent_jobs (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	status TEXT NOT NULL,
	instruction TEXT NOT NULL,
	output_schema_json TEXT,
	input_headers_json TEXT NOT NULL,
	input_csv_path TEXT NOT NULL,
	output_csv_path TEXT NOT NULL,
	auto_export INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	started_at INTEGER,
	completed_at INTEGER,
	last_error TEXT
);`
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
		`INSERT INTO agent_jobs
			(id, name, status, instruction, input_headers_json, input_csv_path, output_csv_path, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"job-1", "fixture-job", "completed", "process rows", `["col"]`,
		FixtureProjectPath()+"/data/input.csv", FixtureProjectPath()+"/data/output.csv",
		now, now,
	)
	if err != nil {
		t.Fatalf("insert fixture agent job: %v", err)
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
