package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/sqlrewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

func fixtureWorkspace(t *testing.T) (*Workspace, *Home) {
	t.Helper()
	home := SetupFixture(t)
	home.AgentsDir = FixtureAgentsDir(t)
	return NewWorkspace(home, fakeGetenv(nil), noProcesses, time.Now), home
}

func planAndApply(t *testing.T, workspace *Workspace, req tool.MoveRequest) (planCounts, applyCounts map[string]int) {
	t.Helper()
	ctx := context.Background()

	planSurfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	planCounts = make(map[string]int, len(planSurfaces))
	for _, surface := range planSurfaces {
		count, err := surface.Plan(ctx)
		require.NoError(t, err, "plan %s", surface.Name)
		planCounts[surface.Name] = count
	}

	applySurfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	undo := tool.NewRestorer()
	applyCounts = make(map[string]int, len(applySurfaces))
	for _, surface := range applySurfaces {
		count, err := surface.Apply(ctx, undo)
		require.NoError(t, err, "apply %s", surface.Name)
		applyCounts[surface.Name] = count
	}
	undo.Cleanup()
	return planCounts, applyCounts
}

func TestMoveSurfacesDryRunApplyCountParity(t *testing.T) {
	workspace, _ := fixtureWorkspace(t)
	req := tool.MoveRequest{
		OldPath:     FixtureProjectPath(),
		NewPath:     "/Users/fixture/renamed-project",
		DeepRewrite: true,
	}

	planCounts, applyCounts := planAndApply(t, workspace, req)

	assert.Equal(t, planCounts, applyCounts, "dry-run and apply must agree exactly on every surface's count")
	assert.Positive(t, planCounts["state-db"])
	assert.Positive(t, planCounts["memories-db"])
	assert.Positive(t, planCounts["config"])
	assert.Positive(t, planCounts[categorySessions])
	assert.Positive(t, planCounts["memories-worktree"])
	assert.Positive(t, planCounts["agents-marketplace"])
}

func TestMoveSurfacesIdempotentSecondApplyFindsNothing(t *testing.T) {
	workspace, _ := fixtureWorkspace(t)
	req := tool.MoveRequest{
		OldPath:     FixtureProjectPath(),
		NewPath:     "/Users/fixture/renamed-project",
		DeepRewrite: true,
	}

	_, applyCounts := planAndApply(t, workspace, req)
	require.Positive(t, applyCounts["state-db"], "sanity: the first apply must have found the project")

	_, err := workspace.MoveSurfaces(req)

	assert.ErrorIs(t, err, tool.ErrProjectAbsent,
		"re-running the same move must converge: the old path is gone, so the tool no longer knows the project")
}

func TestMoveSurfacesReportsProjectAbsentForUnknownProject(t *testing.T) {
	workspace, _ := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: "/Users/fixture/never-seen", NewPath: "/Users/fixture/also-never-seen"}

	_, err := workspace.MoveSurfaces(req)

	assert.ErrorIs(t, err, tool.ErrProjectAbsent)
}

func TestConfigSurfaceDiscoversProfileOverlay(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	files, err := discoverConfigTOMLFiles(home)

	require.NoError(t, err)
	assert.Contains(t, files, filepath.Join(home.Dir, "config.toml"))
	assert.Contains(t, files, filepath.Join(home.Dir, "work.config.toml"))

	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	surfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	var configSurface *tool.Surface
	for index := range surfaces {
		if surfaces[index].Name == "config" {
			configSurface = &surfaces[index]
		}
	}
	require.NotNil(t, configSurface)
	count, err := configSurface.Plan(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, count, "one [projects] key in config.toml plus one in work.config.toml")
}

func TestConfigSurfacePreservesCommentsAndOtherProjectsKey(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}

	undo := tool.NewRestorer()
	surfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	for _, surface := range surfaces {
		if surface.Name != "config" {
			continue
		}
		_, err := surface.Apply(context.Background(), undo)
		require.NoError(t, err)
	}

	data, err := os.ReadFile(filepath.Join(home.Dir, "config.toml"))
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "# Fixture config.toml", "leading comment preserved")
	assert.Contains(t, content, "/Users/fixture/renamed-project", "moved project's key rewritten")
	assert.Contains(t, content, "/Users/fixture/other-project", "unrelated project's key untouched")
}

func TestMemoriesWorktreeGitBaselineDeletedWhenNoRemote(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	gitDir := filepath.Join(home.Dir, memoriesWorktreeSubdir, gitDirName)
	require.DirExists(t, gitDir)

	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	planAndApply(t, workspace, req)

	_, statErr := os.Stat(gitDir)
	assert.True(t, os.IsNotExist(statErr), "a no-remote git baseline is safe to delete: Codex re-initializes it")
	assert.NoDirExists(t, gitDir+".cc-port-rollback.tmp")
}

func TestMemoriesWorktreeGitBaselineRestoresAfterLaterFailure(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	root := filepath.Join(home.Dir, memoriesWorktreeSubdir)
	gitDir := filepath.Join(root, gitDirName)
	undo := tool.NewRestorer()
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}

	_, err := workspace.memoriesWorktreeSurface(req, &pendingMoveDatabases{}).Apply(context.Background(), undo)
	require.NoError(t, err)
	require.NoError(t, undo.Restore())

	assert.DirExists(t, gitDir)
	assert.NoDirExists(t, gitDir+".cc-port-rollback.tmp")
}

func TestMemoriesWorktreeGitBaselineStaysWhenNothingWasRewritten(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "dotcodex")
	root := filepath.Join(homeDir, memoriesWorktreeSubdir)
	gitDir := filepath.Join(root, gitDirName)
	require.NoError(t, os.MkdirAll(gitDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n"), 0o600))
	workspace := NewWorkspace(&Home{Dir: homeDir, SQLiteDir: homeDir}, fakeGetenv(nil), noProcesses, time.Now)
	undo := tool.NewRestorer()
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}

	count, err := workspace.memoriesWorktreeSurface(req, &pendingMoveDatabases{}).Apply(context.Background(), undo)
	require.NoError(t, err)
	assert.Zero(t, count)
	assert.DirExists(t, gitDir)
}

func TestMemoriesWorktreeGitBaselineLeftInPlaceWithRemote(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, gitDirName)
	require.NoError(t, os.MkdirAll(gitDir, 0o750))
	configWithRemote := "[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = https://example.invalid/repo.git\n"
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte(configWithRemote), 0o600))

	safe, err := hasNoRemoteGitBaseline(root)

	require.NoError(t, err)
	assert.False(t, safe)

	_, err = moveGitBaselineToBackup(root, tool.NewRestorer())
	require.NoError(t, err)
	assert.DirExists(t, gitDir, "a git baseline carrying a remote must never be deleted")

	warning, err := memoriesGitBaselineWarning(root)
	require.NoError(t, err)
	assert.NotEmpty(t, warning)
}

func TestMemoriesWorktreeGitBaselineWarningEmptyWhenSafeToDelete(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, gitDirName)
	require.NoError(t, os.MkdirAll(gitDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n\trepositoryformatversion = 0\n"), 0o600))

	warning, err := memoriesGitBaselineWarning(root)

	require.NoError(t, err)
	assert.Empty(t, warning)
}

func TestResidualWarningsReportsEraAAndGitBaselineLeftInPlace(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	// Force the git baseline into the "leave in place" shape so both
	// warning kinds are exercised by one ResidualWarnings call.
	configWithRemote := "[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = https://example.invalid/repo.git\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(home.Dir, memoriesWorktreeSubdir, gitDirName, "config"),
		[]byte(configWithRemote), 0o600,
	))

	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	warnings, err := workspace.ResidualWarnings(req)

	require.NoError(t, err)
	assert.Len(t, warnings, 2, "one era-A warning, one git-baseline-left-in-place warning: %v", warnings)
}

func TestGoalsWarningReportsOnlyPopulatedGoalsDatabases(t *testing.T) {
	sqliteDir := t.TempDir()
	path := filepath.Join(sqliteDir, "goals_1.sqlite")
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { _ = database.Close() }()
	_, err = database.ExecContext(context.Background(), `CREATE TABLE goals (id INTEGER PRIMARY KEY)`)
	require.NoError(t, err)

	warning, err := goalsWarning(sqliteDir)
	require.NoError(t, err)
	assert.Empty(t, warning)

	_, err = database.ExecContext(context.Background(), `INSERT INTO goals (id) VALUES (1)`)
	require.NoError(t, err)
	warning, err = goalsWarning(sqliteDir)
	require.NoError(t, err)
	assert.Equal(t, "goals present, not ported", warning)
}

func TestGoalsWarningIgnoresSQLxMigrationRows(t *testing.T) {
	sqliteDir := t.TempDir()
	path := filepath.Join(sqliteDir, "goals_1.sqlite")
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { _ = database.Close() }()
	_, err = database.ExecContext(context.Background(), `CREATE TABLE _sqlx_migrations (version INTEGER PRIMARY KEY)`)
	require.NoError(t, err)
	_, err = database.ExecContext(context.Background(), `INSERT INTO _sqlx_migrations (version) VALUES (1)`)
	require.NoError(t, err)

	warning, err := goalsWarning(sqliteDir)

	require.NoError(t, err)
	assert.Empty(t, warning)
}

func TestCodexDevWarningRequiresPathReference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-dev.db")
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { _ = database.Close() }()
	_, err = database.ExecContext(context.Background(), `
		CREATE TABLE automations (cwds TEXT);
		CREATE TABLE automation_runs (source_cwd TEXT);
		CREATE TABLE local_thread_catalog (cwd TEXT NOT NULL);
	`)
	require.NoError(t, err)

	warning, err := codexDevWarning(path, FixtureProjectPath())
	require.NoError(t, err)
	assert.Empty(t, warning)

	_, err = database.ExecContext(context.Background(), `INSERT INTO local_thread_catalog (cwd) VALUES (?)`, FixtureProjectPath())
	require.NoError(t, err)
	warning, err = codexDevWarning(path, FixtureProjectPath())
	require.NoError(t, err)
	assert.Equal(t, "codex-dev.db contains path references to the moved project and is never rewritten; refusing to move", warning)
}

func TestResidualWarningsReadsCodexDevFromHomeSQLiteSubdirectory(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	home.SQLiteDir = filepath.Join(t.TempDir(), "redirected-sqlite")
	path := filepath.Join(home.Dir, "sqlite", "codex-dev.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { _ = database.Close() }()
	_, err = database.ExecContext(context.Background(), `
		CREATE TABLE automations (cwds TEXT);
		CREATE TABLE automation_runs (source_cwd TEXT);
		CREATE TABLE local_thread_catalog (cwd TEXT NOT NULL);
		INSERT INTO local_thread_catalog (cwd) VALUES ('/Users/fixture/codexproject');
	`)
	require.NoError(t, err)

	warnings, err := workspace.ResidualWarnings(tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"})

	require.NoError(t, err)
	assert.Contains(t, warnings, "codex-dev.db contains path references to the moved project and is never rewritten; refusing to move")
}

func TestDatabaseTransactionsRollBackBeforeFinalSurface(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	surfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	undo := tool.NewRestorer()
	for _, surface := range surfaces[:2] {
		_, err := surface.Apply(context.Background(), undo)
		require.NoError(t, err)
	}

	require.NoError(t, undo.Restore())
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, stateDBFileName))
	require.NoError(t, err)
	defer func() { _ = database.Close() }()
	var cwd string
	require.NoError(t, database.QueryRowContext(context.Background(), `SELECT cwd FROM threads LIMIT 1`).Scan(&cwd))
	assert.Equal(t, FixtureProjectPath(), cwd)
}

func TestFinalDatabaseSurfaceCommitsAndCheckpoints(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	planAndApply(t, workspace, req)
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, stateDBFileName))
	require.NoError(t, err)
	defer func() { _ = database.Close() }()
	var cwd string
	require.NoError(t, database.QueryRowContext(context.Background(), `SELECT cwd FROM threads LIMIT 1`).Scan(&cwd))
	assert.Equal(t, req.NewPath, cwd)
	if info, statErr := os.Stat(filepath.Join(home.SQLiteDir, stateDBFileName+walSuffix)); statErr == nil {
		assert.Zero(t, info.Size(), "final checkpoint truncates the state database WAL")
	} else {
		assert.True(t, os.IsNotExist(statErr))
	}
}

func TestFinalDatabaseSurfaceReportsSecondCommitPartialStateAndRerunConverges(t *testing.T) {
	workspace, _ := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	pending := &pendingMoveDatabases{removeAll: os.RemoveAll}
	undo := tool.NewRestorer()
	_, err := workspace.stateDBSurface(req, pending).Apply(context.Background(), undo)
	require.NoError(t, err)
	_, err = workspace.memoriesDBSurface(req, pending).Apply(context.Background(), undo)
	require.NoError(t, err)
	pending.state[0].commit = func() error { return assert.AnError }

	_, err = pending.commitSurface().Apply(context.Background(), undo)

	require.Error(t, err)
	assert.Contains(t, err.Error(), pending.state[0].path)
	assert.Contains(t, err.Error(), "partial database commit")
	assert.Contains(t, err.Error(), "re-running the move converges")
	require.NoError(t, undo.Restore())
	memoriesCount, countErr := countMemoriesDB(workspace.home.SQLiteDir, req.OldPath, req.NewPath)
	require.NoError(t, countErr)
	assert.Zero(t, memoriesCount, "the first memories commit must persist before state fails")
	_, err = workspace.MoveSurfaces(req)
	require.NoError(t, err, "state remains the identity source after its commit fails")
	_, applyCounts := planAndApply(t, workspace, req)
	assert.Positive(t, applyCounts["state-db"])
}

func TestFinalDatabaseSurfaceReportsCheckpointFailureAsWarningAfterCommits(t *testing.T) {
	workspace, _ := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	pending := &pendingMoveDatabases{removeAll: os.RemoveAll, reportWarning: workspace.addApplyWarning}
	undo := tool.NewRestorer()
	_, err := workspace.stateDBSurface(req, pending).Apply(context.Background(), undo)
	require.NoError(t, err)
	_, err = workspace.memoriesDBSurface(req, pending).Apply(context.Background(), undo)
	require.NoError(t, err)
	pending.state[0].checkpoint = func() error { return assert.AnError }

	_, err = pending.commitSurface().Apply(context.Background(), undo)

	require.NoError(t, err)
	warnings, warningErr := workspace.ResidualWarnings(req)
	require.NoError(t, warningErr)
	assert.Contains(t, warnings, "could not checkpoint "+pending.state[0].path+" after commit: assert.AnError general error for testing")
}

func TestResidualWarningsKeepsCheckpointWarningWhenLaterScanFails(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	workspace.addApplyWarning("test checkpoint warning")
	badGoalsDB := filepath.Join(home.SQLiteDir, "goals_1.sqlite")
	require.NoError(t, os.WriteFile(badGoalsDB, []byte("not a sqlite database"), 0o600))

	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	warnings, err := workspace.ResidualWarnings(req)

	require.Error(t, err, "a malformed goals database must surface as an error, not be silently swallowed")
	assert.Contains(t, warnings, "test checkpoint warning",
		"the checkpoint warning collected before the scan failure must not be discarded")
}

func TestMoveApplyRefusesWhenCodexDevReferencesMovedProject(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	workspace.now = func() time.Time { return time.Now().Add(time.Hour) }
	createCodexDevDatabase(t, home, `INSERT INTO local_thread_catalog (cwd) VALUES ('/Users/fixture/codexproject')`)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	targets := []tool.Target{{Tool: New(), Workspace: workspace}}

	plan, err := move.DryRun(context.Background(), targets, move.Options{OldPath: req.OldPath, NewPath: req.NewPath})

	require.NoError(t, err)
	assert.Contains(t, plan.ByTool[0].Warnings, "codex-dev.db contains path references to the moved project and is never rewritten; refusing to move")

	result, err := move.Apply(context.Background(), targets, move.Options{OldPath: req.OldPath, NewPath: req.NewPath})

	require.NoError(t, err)
	require.True(t, result.Failed())
	require.Len(t, result.ByTool, 1)
	require.ErrorContains(t, result.ByTool[0].Err, "codex-dev.db contains path references to the moved project")
	assert.Contains(t, result.ByTool[0].Warnings, "codex-dev.db contains path references to the moved project and is never rewritten; refusing to move")
}

func TestMoveApplyProceedsWhenCodexDevReferencesOtherProject(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	workspace.now = func() time.Time { return time.Now().Add(time.Hour) }
	createCodexDevDatabase(t, home, `INSERT INTO local_thread_catalog (cwd) VALUES ('/Users/fixture/other-project')`)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	targets := []tool.Target{{Tool: New(), Workspace: workspace}}

	result, err := move.Apply(context.Background(), targets, move.Options{OldPath: req.OldPath, NewPath: req.NewPath})

	require.NoError(t, err)
	require.False(t, result.Failed())
	assert.True(t, result.ByTool[0].Success)
}

func TestMoveApplyRefusesWhenCodexDevSchemaDrifts(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	workspace.now = func() time.Time { return time.Now().Add(time.Hour) }
	path := filepath.Join(home.Dir, "sqlite", "codex-dev.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	_, err = database.ExecContext(context.Background(), `
		CREATE TABLE automations (cwds TEXT);
		CREATE TABLE automation_runs (source_cwd TEXT);
		CREATE TABLE local_thread_catalog (thread_id TEXT NOT NULL);
	`)
	require.NoError(t, err)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	targets := []tool.Target{{Tool: New(), Workspace: workspace}}

	result, err := move.Apply(context.Background(), targets, move.Options{OldPath: req.OldPath, NewPath: req.NewPath})

	require.NoError(t, err)
	require.True(t, result.Failed())
	require.ErrorContains(t, result.ByTool[0].Err, "codex-dev.db schema drift")
	assert.Contains(t, strings.Join(result.ByTool[0].Warnings, "\n"), "codex-dev.db schema drift")
}

func createCodexDevDatabase(t *testing.T, home *Home, insertStatement string) {
	t.Helper()
	path := filepath.Join(home.Dir, "sqlite", "codex-dev.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	_, err = database.ExecContext(context.Background(), `
		CREATE TABLE automations (cwds TEXT);
		CREATE TABLE automation_runs (source_cwd TEXT);
		CREATE TABLE local_thread_catalog (cwd TEXT NOT NULL);
	`+insertStatement)
	require.NoError(t, err)
}

func TestCountStateDBReadOnlyFailsForMissingAgentJobsColumn(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	_, err = database.ExecContext(context.Background(), `
		CREATE TABLE threads (cwd TEXT NOT NULL);
		CREATE TABLE agent_jobs (id INTEGER PRIMARY KEY, input_csv_path TEXT);
	`)
	require.NoError(t, err)

	_, err = countStateDBReadOnly(database, FixtureProjectPath())

	require.Error(t, err)
	require.ErrorContains(t, err, "required column agent_jobs.output_csv_path is missing")
	require.ErrorContains(t, err, "observed columns: id, input_csv_path")
}

func TestFinalDatabaseSurfaceLeavesBackupAsWarningWhenCleanupFails(t *testing.T) {
	backup := filepath.Join(t.TempDir(), "git.cc-port-rollback.tmp")
	require.NoError(t, os.Mkdir(backup, 0o700))
	pending := &pendingMoveDatabases{gitBackup: backup, removeAll: func(string) error { return assert.AnError }}

	_, err := pending.commitSurface().Apply(context.Background(), tool.NewRestorer())

	require.NoError(t, err)
	assert.DirExists(t, backup)
	warning, err := gitBackupWarning(backup)
	require.NoError(t, err)
	assert.Contains(t, warning, backup)
}

func TestMemoriesRewriteFailureRollsBackStateAndSurfacesRollbackErrors(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	undo := tool.NewRestorer()
	state, _, err := startStateDBRewrites(workspace.home.SQLiteDir, FixtureProjectPath(), "/Users/fixture/renamed-project", undo)
	require.NoError(t, err)
	_, _, err = startDatabaseRewrites(
		workspace.home.SQLiteDir, memoriesDBGlob, FixtureProjectPath(), "/Users/fixture/renamed-project",
		func(database *sqlrewrite.DB, _ *sqlrewrite.Tx, _, _ string) (int, error) {
			require.NoError(t, database.Close())
			return 0, assert.AnError
		}, undo,
	)
	require.ErrorIs(t, err, assert.AnError)
	require.NoError(t, state[0].transaction.Rollback())

	restoreErr := undo.Restore()

	require.Error(t, restoreErr)
	assert.Contains(t, restoreErr.Error(), "rollback")
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, stateDBFileName))
	require.NoError(t, err)
	defer func() { _ = database.Close() }()
	var cwd string
	require.NoError(t, database.QueryRowContext(context.Background(), `SELECT cwd FROM threads LIMIT 1`).Scan(&cwd))
	assert.Equal(t, FixtureProjectPath(), cwd)
}

func TestPlanningLeavesDatabaseAndWALBytesUntouched(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	path := filepath.Join(home.SQLiteDir, stateDBFileName)
	writer, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { _ = writer.Close() }()
	_, err = writer.ExecContext(context.Background(), `PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; UPDATE threads SET title = 'fixture title'`)
	require.NoError(t, err)
	beforeDatabase, err := os.ReadFile(path) //nolint:gosec // G304: fixture path is test-controlled
	require.NoError(t, err)
	beforeWAL, err := os.ReadFile(path + walSuffix) //nolint:gosec // G304: fixture path is test-controlled
	require.NoError(t, err)

	surfaces, err := workspace.MoveSurfaces(tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"})
	require.NoError(t, err)
	for _, surface := range surfaces {
		_, err := surface.Plan(context.Background())
		require.NoError(t, err)
	}

	afterDatabase, err := os.ReadFile(path) //nolint:gosec // G304: fixture path is test-controlled
	require.NoError(t, err)
	afterWAL, err := os.ReadFile(path + walSuffix) //nolint:gosec // G304: fixture path is test-controlled
	require.NoError(t, err)
	assert.Equal(t, beforeDatabase, afterDatabase)
	assert.Equal(t, beforeWAL, afterWAL)
}

func TestAgentsMarketplaceSurfaceSkippedWhenAgentsDirAbsent(t *testing.T) {
	home := SetupFixture(t)
	workspace := NewWorkspace(home, fakeGetenv(nil), noProcesses, time.Now)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}

	surfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	for _, surface := range surfaces {
		if surface.Name != "agents-marketplace" {
			continue
		}
		count, err := surface.Plan(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	}
}
