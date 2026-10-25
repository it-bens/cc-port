// Package codextest provides the Codex adapter's test fixture: a staged
// ~/.codex tree with its SQLite databases and memory worktree git baselines
// built at runtime, plus helpers that read and write the staged state
// database. It does not import package codex, because codex's own tests
// import it; the helpers take and return directories, and callers build the
// codex.Home from them.
package codextest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	// Registers the "sqlite" database/sql driver used below.
	_ "modernc.org/sqlite"

	"github.com/it-bens/cc-port/internal/fsutil"
	"github.com/it-bens/cc-port/internal/testmain"
	"github.com/it-bens/cc-port/internal/tool/codex/codexschema"
)

// FixtureProjectPath returns the canonical project path the fixture tree
// is keyed on: config.toml's [projects] table, every rollout's
// session_meta/turn_context cwd, and the fixture SQLite rows SetupFixture
// builds all reference it.
func FixtureProjectPath() string {
	return "/Users/fixture/codexproject"
}

// templateDir is the staged fixture installTemplate installed. A package's
// TestMain installs it before its tests start and tests only read it, so
// nothing guards it.
var templateDir string

// RunWithTemplate installs the fixture template, runs run, and removes the
// template, returning the exit code for TestMain to pass to os.Exit.
func RunWithTemplate(run func() int) int {
	return testmain.Run(installTemplate, run)
}

// installTemplate stages the fixture template once into a temporary directory,
// for RunWithTemplate to install before the tests start. Every later
// SetupFixture and CopyTemplate copies that template, so a package that
// installs it builds the SQLite databases and git baselines once per test
// binary instead of once per test. The returned cleanup removes the template
// directory.
func installTemplate() (cleanup func() error, err error) {
	return testmain.StageTempDir("cc-port-codex-fixture-", func(dir string) error {
		if err := StageFixture(dir); err != nil {
			return err
		}
		templateDir = dir
		return nil
	})
}

// StageFixture stages the fixture into dir: internal/tool/codex/testdata/dotcodex
// is copied there and fixture state_5.sqlite, memories_1.sqlite, and
// queue_1.sqlite databases plus a no-remote .git baseline under each memory
// worktree root (memories/ and memories_v2/) are built alongside — SQLite
// files are binary and nested .git directories are untrackable by the outer
// repo, so all of it is built by test code rather than committed, following
// the claudetest.SetupFixture pattern. dir becomes a codex home.
func StageFixture(dir string) error {
	fixtureDir, err := fixtureSourceDir()
	if err != nil {
		return err
	}
	if err := fsutil.CopyDir(context.Background(), filepath.Join(fixtureDir, "dotcodex"), dir, nil); err != nil {
		return fmt.Errorf("copy fixture directory: %w", err)
	}

	if err := buildFixtureStateDB(filepath.Join(dir, codexschema.StateDBFileName)); err != nil {
		return err
	}
	if err := buildFixtureMemoriesDB(filepath.Join(dir, codexschema.MemoriesDBFileName)); err != nil {
		return err
	}
	if err := buildFixtureQueueDB(filepath.Join(dir, codexschema.QueueDBFileName)); err != nil {
		return err
	}
	if err := buildMemoriesGitBaseline(filepath.Join(dir, codexschema.MemoriesWorktreeSubdir), FixtureGitConfigNoRemote); err != nil {
		return err
	}
	return buildMemoriesV2Worktree(filepath.Join(dir, codexschema.MemoriesWorktreeV2Subdir))
}

// SetupFixture copies the fixture template RunWithTemplate installed into a
// fresh t.TempDir() and returns the staged codex home directory, which is
// also the fixture's SQLite directory: sqlite_home resolves to the same
// directory (config.toml declares no sqlite_home key and $CODEX_SQLITE_HOME
// is unset in the fixture environment).
func SetupFixture(t testing.TB) string {
	t.Helper()

	codexDir := filepath.Join(t.TempDir(), "dotcodex")
	if err := CopyTemplate(codexDir); err != nil {
		t.Fatal(err)
	}
	return codexDir
}

// CopyTemplate copies the fixture template RunWithTemplate installed into dir,
// which becomes a codex home. It takes no testing.TB, so a once-function
// building a package-wide fixture can stage its home from the template.
func CopyTemplate(dir string) error {
	if templateDir == "" {
		return errors.New("no codextest fixture template installed: call codextest.RunWithTemplate " +
			"from the package's TestMain")
	}
	if err := fsutil.CopyDir(context.Background(), templateDir, dir, nil); err != nil {
		return fmt.Errorf("copy fixture template: %w", err)
	}
	return nil
}

// InsertThreadRow inserts a threads row with id and cwd directly into the
// fixture state database under sqliteDir, for tests exercising a project
// SetupFixture's canned fixture does not cover, such as a symlink-aliased
// cwd built under t.TempDir() by a cross-package end-to-end move test.
func InsertThreadRow(t *testing.T, sqliteDir, id, cwd string) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(sqliteDir, codexschema.StateDBFileName))
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

// ThreadCWD reads back threads.cwd for id from the fixture state database
// under sqliteDir, for tests asserting a move rewrote it.
func ThreadCWD(t *testing.T, sqliteDir, id string) string {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(sqliteDir, codexschema.StateDBFileName))
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

// fixtureSourceDir walks up from the working directory to the committed
// testdata tree every staged fixture copies from.
func fixtureSourceDir() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	for {
		candidate := filepath.Join(currentDir, "internal", "tool", "codex", "testdata")
		if info, statErr := os.Stat(filepath.Join(candidate, "dotcodex")); statErr == nil && info.IsDir() {
			return candidate, nil
		}
		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return "", errors.New("could not find testdata/dotcodex/ directory")
		}
		currentDir = parentDir
	}
}

// FixtureGitConfigNoRemote is a local-only baseline config, the shape the
// codex adapter's hasNoRemoteGitBaseline probe accepts.
const FixtureGitConfigNoRemote = "[core]\n\trepositoryformatversion = 0\n"

// FixtureGitConfigWithRemote attaches the baseline to a remote, the shape a
// move leaves in place.
const FixtureGitConfigWithRemote = FixtureGitConfigNoRemote +
	"[remote \"origin\"]\n\turl = https://example.invalid/repo.git\n"

// BuildFixtureMemoriesGitBaseline creates a .git baseline under root whose
// config is config, at runtime: git never tracks a nested .git directory, so
// — like the SQLite fixtures below — test code builds it instead of
// committing it. Calling it on a root that already has a baseline replaces
// that baseline's config.
func BuildFixtureMemoriesGitBaseline(t testing.TB, root, config string) {
	t.Helper()
	if err := buildMemoriesGitBaseline(root, config); err != nil {
		t.Fatalf("build fixture memories git baseline: %v", err)
	}
}

func buildMemoriesGitBaseline(root, config string) error {
	gitDir := filepath.Join(root, codexschema.GitDirName)
	if err := os.MkdirAll(gitDir, 0o750); err != nil {
		return fmt.Errorf("create fixture memories git baseline: %w", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(config), 0o600); err != nil {
		return fmt.Errorf("write fixture memories git baseline config: %w", err)
	}
	return nil
}

// buildMemoriesV2Worktree stages a memories_v2/ worktree mirroring
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
func buildMemoriesV2Worktree(root string) error {
	summariesDir := filepath.Join(root, "rollout_summaries")
	if err := os.MkdirAll(summariesDir, 0o750); err != nil {
		return fmt.Errorf("create fixture memories_v2 worktree: %w", err)
	}

	summary := "thread_id: 00000000-0000-4000-8000-000000000001\n" +
		"cwd: " + FixtureProjectPath() + "\n\n" +
		"Summary: fixed a bug in " + FixtureProjectPath() + "/src/main.py.\n"
	if err := os.WriteFile(filepath.Join(summariesDir, "2026-07-17T10-00-00-a1b2.md"), []byte(summary), 0o600); err != nil {
		return fmt.Errorf("write fixture memories_v2 rollout summary: %w", err)
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
		return fmt.Errorf("write fixture memories_v2 memory_summary.md: %w", err)
	}

	return buildMemoriesGitBaseline(root, FixtureGitConfigNoRemote)
}

// FixtureProjectID is the projects.id of the fixture's one Codex project,
// whose single project_roots row holds FixtureProjectPath.
const FixtureProjectID = "primary-project"

// BuildFixtureStateDB creates the fixture state database at path: the
// codexschema.StateDBSchema tables holding one thread whose cwd is
// FixtureProjectPath, the FixtureProjectID project with its one root, and a
// complete backfill_state row.
func BuildFixtureStateDB(t testing.TB, path string) {
	t.Helper()
	if err := buildFixtureStateDB(path); err != nil {
		t.Fatalf("build fixture state db: %v", err)
	}
}

func buildFixtureStateDB(path string) error {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open fixture state db: %w", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.ExecContext(context.Background(), codexschema.StateDBSchema); err != nil {
		return fmt.Errorf("create fixture state schema: %w", err)
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
		return fmt.Errorf("insert fixture thread: %w", err)
	}

	_, err = database.ExecContext(context.Background(),
		`INSERT INTO projects (id, name, metadata, position, created_at_ms, updated_at_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		FixtureProjectID, "codexproject", "{}", 0, now*1000, now*1000,
	)
	if err != nil {
		return fmt.Errorf("insert fixture project: %w", err)
	}
	_, err = database.ExecContext(context.Background(),
		`INSERT INTO project_roots (project_id, position, path) VALUES (?, ?, ?)`,
		FixtureProjectID, 0, FixtureProjectPath(),
	)
	if err != nil {
		return fmt.Errorf("insert fixture project root: %w", err)
	}

	const backfillEpoch = 1_752_137_200
	_, err = database.ExecContext(context.Background(),
		`INSERT INTO backfill_state (id, status, last_watermark, last_success_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`,
		1, "complete", "sessions/2026/07/17/rollout-2026-07-17T10-00-00-00000000-0000-4000-8000-000000000001.jsonl", backfillEpoch, backfillEpoch,
	)
	if err != nil {
		return fmt.Errorf("insert fixture backfill state: %w", err)
	}
	return nil
}

func buildFixtureMemoriesDB(path string) error {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open fixture memories db: %w", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.ExecContext(context.Background(), codexschema.MemoriesDBSchema); err != nil {
		return fmt.Errorf("create fixture memories schema: %w", err)
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
		return fmt.Errorf("insert fixture stage1 output: %w", err)
	}
	return nil
}

// FixtureQueuedItemID and FixtureQueuedMentionItemID are the queued_items
// rows SetupFixture seeds.
const (
	FixtureQueuedItemID        = "fixture-queued-skill-item"
	FixtureQueuedMentionItemID = "fixture-queued-mention-item"
)

// BuildFixtureQueueDB creates the fixture queue database at path: the
// codexschema.QueueDBSchema table holding two queued turns whose payload_json
// is the serde_json shape of TurnInput::UserInput, one carrying a
// UserInput::Skill and one a UserInput::Mention of a SKILL.md file in the
// fixture project.
func BuildFixtureQueueDB(t testing.TB, path string) {
	t.Helper()
	if err := buildFixtureQueueDB(path); err != nil {
		t.Fatalf("build fixture queue db: %v", err)
	}
}

// buildFixtureQueueDB writes two queued turns whose payload_json is the
// serde_json shape of TurnInput::UserInput: one carrying a UserInput::Skill
// that points into the fixture project, one carrying a UserInput::Mention of
// a SKILL.md file in the fixture project.
func buildFixtureQueueDB(path string) error {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open fixture queue db: %w", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.ExecContext(context.Background(), codexschema.QueueDBSchema); err != nil {
		return fmt.Errorf("create fixture queue schema: %w", err)
	}

	payloads := []struct{ id, payload string }{
		{FixtureQueuedItemID, `{"UserInput":{"content":[{"type":"skill","name":"deploy","path":"` +
			FixtureProjectPath() + `/.codex/skills/deploy/SKILL.md"}],"client_id":"fixture-client"}}`},
		{FixtureQueuedMentionItemID, `{"UserInput":{"content":[{"type":"mention","name":"release","path":"` +
			FixtureProjectPath() + `/.codex/skills/release/SKILL.md"}],"client_id":"fixture-client"}}`},
	}
	now := time.Now().UnixMilli()
	for order, item := range payloads {
		_, err = database.ExecContext(context.Background(),
			`INSERT INTO queued_items (id, thread_id, payload_json, queue_order, created_at_ms, updated_at_ms)
				VALUES (?, ?, ?, ?, ?, ?)`,
			item.id, "00000000-0000-4000-8000-000000000001", item.payload, order, now, now,
		)
		if err != nil {
			return fmt.Errorf("insert fixture queued item %s: %w", item.id, err)
		}
	}
	return nil
}
