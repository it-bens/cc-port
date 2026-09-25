package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/sqlrewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

func fixtureWorkspace(t *testing.T) (*Workspace, *Home) {
	t.Helper()
	home := SetupFixture(t)
	home.AgentsDir = FixtureAgentsDir(t)
	return NewWorkspace(home, fakeGetenv(nil), noProcesses), home
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
		planCounts[surface.Name] = count.Count
	}

	applySurfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	undo := tool.NewRestorer()
	applyCounts = make(map[string]int, len(applySurfaces))
	for _, surface := range applySurfaces {
		count, err := surface.Apply(ctx, undo)
		require.NoError(t, err, "apply %s", surface.Name)
		applyCounts[surface.Name] = count.Count
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

// TestMoveSurfacesRewritesBothMemoryWorktreeRoots guards that the
// "memories-worktree" surface covers memories_v2/ alongside memories/: both
// roots carry the fixture's project reference, so both must be rewritten to
// newPath and neither may retain oldPath.
func TestMoveSurfacesRewritesBothMemoryWorktreeRoots(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project", DeepRewrite: true}

	planAndApply(t, workspace, req)

	v1Data, err := os.ReadFile(filepath.Join(home.Dir, memoriesWorktreeSubdir, "raw_memories.md"))
	require.NoError(t, err)
	assert.Contains(t, string(v1Data), req.NewPath)
	assert.NotContains(t, string(v1Data), req.OldPath)

	v2SummaryData, err := os.ReadFile(
		filepath.Join(home.Dir, memoriesWorktreeV2Subdir, "rollout_summaries", "2026-07-17T10-00-00-a1b2.md"),
	)
	require.NoError(t, err)
	assert.Contains(t, string(v2SummaryData), req.NewPath, "the memories_v2 worktree root must be rewritten alongside memories/")
	assert.NotContains(t, string(v2SummaryData), req.OldPath)

	v2MemorySummaryData, err := os.ReadFile(filepath.Join(home.Dir, memoriesWorktreeV2Subdir, "memory_summary.md"))
	require.NoError(t, err)
	assert.Contains(t, string(v2MemorySummaryData), req.NewPath)
	assert.NotContains(t, string(v2MemorySummaryData), req.OldPath)

	// v2 never writes raw_memories.md: memories/write/src/phase2.rs's
	// sync_phase2_workspace_inputs calls rebuild_raw_memories_file_from_memories
	// only for MemoryVersion::V1.
	_, statErr := os.Stat(filepath.Join(home.Dir, memoriesWorktreeV2Subdir, "raw_memories.md"))
	assert.True(t, os.IsNotExist(statErr), "memories_v2/ must never carry raw_memories.md")
}

func TestMoveSurfacesRolloutPlanApplyCountParity(t *testing.T) {
	workspace, _ := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project", DeepRewrite: true}

	planCounts, applyCounts := planAndApply(t, workspace, req)

	require.Contains(t, planCounts, categorySessions)
	assert.Equal(t, planCounts[categorySessions], applyCounts[categorySessions])
}

func TestMoveSurfacesRefusesCompressedOnlyRollout(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	path := filepath.Join(home.Dir, sessionsSubdir, "2026", "07", "18", "rollout-refused.jsonl.zst")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("junk"), 0o600))

	_, err := workspace.MoveSurfaces(tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"})

	require.ErrorIs(t, err, ErrCompressedRolloutUnsupported)
	assert.Contains(t, err.Error(), path)
}

// TestMove_RewritesSymlinkAliasedThreadCwd guards finding H1 (spec §5.1): a
// thread row recorded through a symlink-aliased cwd (Codex stores
// config.cwd() verbatim, uncanonicalized) must still be matched and rewritten
// by move, and the dry-run count must equal the number of rows apply
// actually rewrites, since the state-db Plan counts the plan
// matchingPathRewrites captured and Apply writes that same plan.
func TestMove_RewritesSymlinkAliasedThreadCwd(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	tempRoot := t.TempDir()
	realProject := filepath.Join(tempRoot, "real", "project")
	require.NoError(t, os.MkdirAll(realProject, 0o750))
	require.NoError(t, os.Symlink(filepath.Join(tempRoot, "real"), filepath.Join(tempRoot, "link")))
	aliasedCWD := filepath.Join(tempRoot, "link", "project")
	newPath := filepath.Join(tempRoot, "real", "renamed-project")

	const aliasedThreadID = "00000000-0000-4000-8000-0000000000aa"
	insertThreadRowForProject(t, filepath.Join(home.SQLiteDir, stateDBFileName), aliasedThreadID, aliasedCWD, threadRowMetadata{})

	req := tool.MoveRequest{OldPath: realProject, NewPath: newPath}
	planCounts, applyCounts := planAndApply(t, workspace, req)

	require.Positive(t, planCounts["state-db"], "sanity: the symlink-aliased thread must be counted")
	assert.Equal(t, planCounts["state-db"], applyCounts["state-db"],
		"dry-run count and apply must consume the same canonical-match computation (spec §5.1)")

	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, stateDBFileName))
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	var storedCWD string
	require.NoError(t, database.QueryRowContext(
		context.Background(), `SELECT cwd FROM threads WHERE id = ?`, aliasedThreadID,
	).Scan(&storedCWD))
	assert.Equal(t, newPath, storedCWD, "the stored cwd must be rewritten to the literal new path, not left as the symlink alias")
}

func TestMoveSurfaces_UsesPreflightThreadMatchesAfterSourceRemoved(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	tempRoot := t.TempDir()
	realProject := filepath.Join(tempRoot, "real", "project")
	require.NoError(t, os.MkdirAll(realProject, 0o750))
	require.NoError(t, os.Symlink(filepath.Join(tempRoot, "real"), filepath.Join(tempRoot, "link")))
	aliasedCWD := filepath.Join(tempRoot, "link", "project")
	newPath := filepath.Join(tempRoot, "renamed-project")
	const threadID = "00000000-0000-4000-8000-0000000000dd"
	insertThreadRowForProject(t, filepath.Join(home.SQLiteDir, stateDBFileName), threadID, aliasedCWD, threadRowMetadata{})

	surfaces, err := workspace.MoveSurfaces(tool.MoveRequest{OldPath: realProject, NewPath: newPath})
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(realProject), "simulate Claude's earlier apply removing the source")
	undo := tool.NewRestorer()
	for _, surface := range surfaces {
		_, err := surface.Apply(context.Background(), undo)
		require.NoError(t, err, "apply %s", surface.Name)
	}
	undo.Cleanup()

	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, stateDBFileName))
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	var cwd string
	require.NoError(t, database.QueryRowContext(context.Background(), `SELECT cwd FROM threads WHERE id = ?`, threadID).Scan(&cwd))
	assert.Equal(t, newPath, cwd)
}

func TestMove_ConfigSymlinkAliasRewritesStoredTrustKey(t *testing.T) {
	homeDir := t.TempDir()
	realProject := filepath.Join(homeDir, "real", "project")
	require.NoError(t, os.MkdirAll(realProject, 0o750))
	require.NoError(t, os.Symlink(filepath.Join(homeDir, "real"), filepath.Join(homeDir, "link")))
	alias := filepath.Join(homeDir, "link", "project")
	newPath := filepath.Join(homeDir, "renamed-project")
	config := "# retain\n[projects.\"" + alias + "\"]\ntrust_level = \"trusted\"\n[projects.\"/other\"]\ntrust_level = \"trusted\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, configTOMLFileName), []byte(config), 0o600))
	workspace := NewWorkspace(
		&Home{Dir: homeDir, SQLiteDir: filepath.Join(homeDir, "sqlite")}, fakeGetenv(nil), noProcesses,
	)

	surfaces, err := workspace.MoveSurfaces(tool.MoveRequest{OldPath: realProject, NewPath: newPath})
	require.NoError(t, err)
	undo := tool.NewRestorer()
	for _, surface := range surfaces {
		if surface.Name == "config" {
			_, err = surface.Apply(context.Background(), undo)
			require.NoError(t, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(homeDir, configTOMLFileName)) //nolint:gosec // G304: path under t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(data), "# retain")
	assert.Contains(t, string(data), newPath)
	assert.Contains(t, string(data), "/other")
	assert.NotContains(t, string(data), alias)
}

func TestMoveSurfacesReportsProjectAbsentForUnknownProject(t *testing.T) {
	workspace, _ := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: "/Users/fixture/never-seen", NewPath: "/Users/fixture/also-never-seen"}

	_, err := workspace.MoveSurfaces(req)

	assert.ErrorIs(t, err, tool.ErrProjectAbsent)
}

// TestMoveSurfaces_ReturnsUnresolvedErrorWhenProfileOverlayDiverges is the
// move-side counterpart to the export-family test of the same name in
// export_import_stats_test.go: MoveSurfaces must not report a bare
// tool.ErrProjectAbsent for a project unknown to every source this adapter
// checks under the base-resolved SQLiteDir when a profile overlay declares
// a different sqlite_home this adapter cannot resolve against.
func TestMoveSurfaces_ReturnsUnresolvedErrorWhenProfileOverlayDiverges(t *testing.T) {
	workspace, project := divergentProfileUnknownProjectFixture(t)
	req := tool.MoveRequest{OldPath: project, NewPath: "/Users/fixture/renamed-elsewhere"}

	_, err := workspace.MoveSurfaces(req)

	require.ErrorIs(t, err, ErrProjectAbsenceUnresolved)
	assert.NotErrorIs(t, err, tool.ErrProjectAbsent,
		"a divergent profile overlay must not be reported as bare absence")
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
	assert.Equal(t, 2, count.Count, "one [projects] key in config.toml plus one in work.config.toml")
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

func TestApplyConfigTOMLFileRewritesProjectLocalHookStateKey(t *testing.T) {
	oldPath := FixtureProjectPath()
	newPath := "/Users/fixture/renamed-project"
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "[projects.\"" + oldPath + "\"]\ntrust_level = \"trusted\"\n\n" +
		"[hooks.state.\"" + oldPath + "/.codex/hooks.toml:pre_tool_use:0:0\"]\n" +
		"enabled = true\ntrusted_hash = \"sha256:abc\"\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	count, err := applyConfigTOMLFile(path, oldPath, newPath, tool.NewRestorer())

	require.NoError(t, err)
	assert.Equal(t, 2, count, "the projects key and the project-local hooks.state key both rewrite")
	rewritten, err := os.ReadFile(path) //nolint:gosec // G304: path under t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(rewritten),
		"[hooks.state.\""+newPath+"/.codex/hooks.toml:pre_tool_use:0:0\"]",
		"the project-local hook trust key is relocated with the project")
	assert.NotContains(t, string(rewritten), oldPath, "no reference to the old project path survives")
}

func TestWorktreeFiles_ExcludesArtifacts(t *testing.T) {
	root := t.TempDir()
	realFile := filepath.Join(root, "raw_memories.md")
	require.NoError(t, os.WriteFile(realFile, []byte("real"), 0o600))
	staleSibling := filepath.Join(root, "raw_memories.md"+rewrite.RollbackSuffix)
	require.NoError(t, os.WriteFile(staleSibling, []byte("stale"), 0o600))
	strandedGitBackup := filepath.Join(root, gitDirName+gitBackupSuffix)
	require.NoError(t, os.MkdirAll(strandedGitBackup, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(strandedGitBackup, "config"), []byte("[core]"), 0o600))

	files, err := worktreeFiles(root)

	require.NoError(t, err)
	assert.Equal(t, []string{realFile}, files, "worktree discovery must ignore cc-port's own artifacts, file- or directory-shaped")
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

// TestMemoriesWorktreeSurfaceApply_V2FailureRestoresBothRoots guards
// cross-root rollback: memories/ is rewritten and its baseline moved to
// backup first (it sorts before memories_v2 in memoriesWorktreeSubdirs),
// then memories_v2/'s apply fails partway through its own worktree walk.
// Restoring after that failure must put BOTH roots back exactly as found:
// memories/'s rewritten file and its invalidated baseline, and
// memories_v2/'s own already-rewritten file, not just the root that failed.
func TestMemoriesWorktreeSurfaceApply_V2FailureRestoresBothRoots(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	v1RawMemories := filepath.Join(home.Dir, memoriesWorktreeSubdir, "raw_memories.md")
	originalV1, err := os.ReadFile(v1RawMemories) //nolint:gosec // G304: fixture path is test-controlled
	require.NoError(t, err)
	v1GitDir := filepath.Join(home.Dir, memoriesWorktreeSubdir, gitDirName)

	v2MemorySummary := filepath.Join(home.Dir, memoriesWorktreeV2Subdir, "memory_summary.md")
	originalV2, err := os.ReadFile(v2MemorySummary) //nolint:gosec // G304: fixture path is test-controlled
	require.NoError(t, err)
	// worktreeFiles walks memory_summary.md before rollout_summaries/ (lexical
	// order), so making the rollout summary unreadable lets memory_summary.md
	// get rewritten first, leaving real partial state in memories_v2/ to roll
	// back once the walk fails on this file.
	unreadable := filepath.Join(home.Dir, memoriesWorktreeV2Subdir, "rollout_summaries", "2026-07-17T10-00-00-a1b2.md")
	require.NoError(t, os.Chmod(unreadable, 0o000))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })

	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	undo := tool.NewRestorer()
	pending := &pendingMoveDatabases{removeAll: os.RemoveAll}

	_, err = workspace.memoriesWorktreeSurface(req, pending).Apply(context.Background(), undo)
	require.Error(t, err, "the unreadable memories_v2 file must fail the surface apply")

	require.NoError(t, undo.Restore())

	restoredV1, err := os.ReadFile(v1RawMemories) //nolint:gosec // G304: fixture path is test-controlled
	require.NoError(t, err)
	assert.Equal(t, originalV1, restoredV1, "memories/'s rewritten file must be restored")
	assert.DirExists(t, v1GitDir, "memories/'s invalidated baseline must be restored back in place")

	restoredV2, err := os.ReadFile(v2MemorySummary) //nolint:gosec // G304: fixture path is test-controlled
	require.NoError(t, err)
	assert.Equal(t, originalV2, restoredV2, "memories_v2/'s already-rewritten file must be restored too")
}

func TestMemoriesWorktreeGitBaselineStaysWhenNothingWasRewritten(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "dotcodex")
	root := filepath.Join(homeDir, memoriesWorktreeSubdir)
	gitDir := filepath.Join(root, gitDirName)
	require.NoError(t, os.MkdirAll(gitDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n"), 0o600))
	workspace := NewWorkspace(&Home{Dir: homeDir, SQLiteDir: homeDir}, fakeGetenv(nil), noProcesses)
	undo := tool.NewRestorer()
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}

	count, err := workspace.memoriesWorktreeSurface(req, &pendingMoveDatabases{}).Apply(context.Background(), undo)
	require.NoError(t, err)
	assert.Zero(t, count.Count)
	assert.DirExists(t, gitDir)
}

// TestMemoriesWorktreeSurface_AbsentV2RootTreatedLikeAbsentSurface guards
// that an absent memories_v2/ root is tolerated exactly like an absent
// memories/ root is today: a legitimate "surface not present", never an
// error, and never fabricated on disk.
func TestMemoriesWorktreeSurface_AbsentV2RootTreatedLikeAbsentSurface(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "dotcodex")
	root := filepath.Join(homeDir, memoriesWorktreeSubdir)
	require.NoError(t, os.MkdirAll(root, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "raw_memories.md"), []byte("Notes about "+FixtureProjectPath()+".\n"), 0o600,
	))
	workspace := NewWorkspace(&Home{Dir: homeDir, SQLiteDir: homeDir}, fakeGetenv(nil), noProcesses)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	undo := tool.NewRestorer()
	pending := &pendingMoveDatabases{removeAll: os.RemoveAll}

	count, err := workspace.memoriesWorktreeSurface(req, pending).Apply(context.Background(), undo)

	require.NoError(t, err, "an absent memories_v2/ root must not error")
	assert.Equal(t, 1, count.Count, "only the present memories/ root contributes to the count")
	_, statErr := os.Stat(filepath.Join(homeDir, memoriesWorktreeV2Subdir))
	assert.True(t, os.IsNotExist(statErr), "no memories_v2/ directory is fabricated for an absent root")
}

// TestMemoriesWorktree_ConvergentRerunInvalidatesBaseline guards finding
// A6: a convergent re-run whose rewrite already happened in an earlier,
// interrupted apply has THIS run's own rewrite count at zero (the
// worktree file already holds newPath, not oldPath), which the old
// count==0 gate misread as "nothing changed, leave the baseline alone."
// The fixed gate checks the worktree's PERSISTENT post-rewrite state, so
// it still invalidates a baseline that predates newPath's appearance even
// when this particular run touched no bytes.
func TestMemoriesWorktree_ConvergentRerunInvalidatesBaseline(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "dotcodex")
	root := filepath.Join(homeDir, memoriesWorktreeSubdir)
	gitDir := filepath.Join(root, gitDirName)
	buildFixtureMemoriesGitBaseline(t, root, fixtureGitConfigNoRemote)
	newPath := "/Users/fixture/renamed-project"
	// A local-only (no remote) baseline, and a worktree file already
	// rewritten by an earlier, interrupted apply: it holds newPath, not
	// oldPath, so this run's own applyMemoriesWorktree count is zero.
	require.NoError(t, os.WriteFile(filepath.Join(root, "raw_memories.md"), []byte("Notes about "+newPath+".\n"), 0o600))
	workspace := NewWorkspace(&Home{Dir: homeDir, SQLiteDir: homeDir}, fakeGetenv(nil), noProcesses)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: newPath}
	undo := tool.NewRestorer()

	count, err := workspace.memoriesWorktreeSurface(req, &pendingMoveDatabases{}).Apply(context.Background(), undo)

	require.NoError(t, err)
	assert.Zero(t, count.Count, "sanity: this run's own rewrite touches nothing since the worktree already holds newPath")
	_, statErr := os.Stat(gitDir)
	assert.True(t, os.IsNotExist(statErr),
		"a convergent re-run must still invalidate a stale baseline, not only a run that itself rewrote bytes")
	assert.DirExists(t, gitDir+gitBackupSuffix, "the baseline must be moved to a rollback backup, not deleted outright")
}

func TestMemoriesWorktreeGitBaselineLeftInPlaceWithRemote(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, gitDirName)
	buildFixtureMemoriesGitBaseline(t, root, fixtureGitConfigWithRemote)
	newPath := "/Users/fixture/renamed-project"
	// The worktree already references newPath, so the remote is the only
	// gate that can keep the baseline in place.
	require.NoError(t, os.WriteFile(filepath.Join(root, "raw_memories.md"), []byte("Notes about "+newPath+".\n"), 0o600))

	safe, err := hasNoRemoteGitBaseline(root)

	require.NoError(t, err)
	assert.False(t, safe)

	_, err = moveGitBaselineToBackup(root, newPath, tool.NewRestorer())
	require.NoError(t, err)
	assert.DirExists(t, gitDir, "a git baseline carrying a remote must never be deleted")

	warning, err := memoriesGitBaselineWarning(root, FixtureProjectPath(), newPath)
	require.NoError(t, err)
	assert.NotEmpty(t, warning)
}

// TestMemoriesWorktreeGitBaselineRefusalIsPerRoot guards that the
// remote-carrying refusal rule evaluates each memory worktree root
// independently: memories/.git carrying a remote must stay in place while
// memories_v2/.git, still the fixture's default no-remote baseline, is
// still invalidated.
func TestMemoriesWorktreeGitBaselineRefusalIsPerRoot(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	buildFixtureMemoriesGitBaseline(t, filepath.Join(home.Dir, memoriesWorktreeSubdir), fixtureGitConfigWithRemote)

	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	planAndApply(t, workspace, req)

	assert.DirExists(t, filepath.Join(home.Dir, memoriesWorktreeSubdir, gitDirName),
		"memories/.git carries a remote and must be left in place")
	_, statErr := os.Stat(filepath.Join(home.Dir, memoriesWorktreeV2Subdir, gitDirName))
	assert.True(t, os.IsNotExist(statErr),
		"memories_v2/.git has no remote and must still be invalidated independently of memories/.git's refusal")

	warnings, err := workspace.ResidualWarnings(req)
	require.NoError(t, err)
	assert.Contains(t, warnings, "memories/.git carries a remote and was left in place; its worktree contents were rewritten",
		"the warning must name the root that actually kept its baseline, not memories_v2")
}

// TestMemoriesWorktreeGitBaselineWarningNamesV2Root is the symmetric case of
// TestMemoriesWorktreeGitBaselineRefusalIsPerRoot: a remote in
// memories_v2/.git must be preserved and its warning must name memories_v2,
// not memories.
func TestMemoriesWorktreeGitBaselineWarningNamesV2Root(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	buildFixtureMemoriesGitBaseline(t, filepath.Join(home.Dir, memoriesWorktreeV2Subdir), fixtureGitConfigWithRemote)

	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	planAndApply(t, workspace, req)

	assert.DirExists(t, filepath.Join(home.Dir, memoriesWorktreeV2Subdir, gitDirName),
		"memories_v2/.git carries a remote and must be left in place")
	_, statErr := os.Stat(filepath.Join(home.Dir, memoriesWorktreeSubdir, gitDirName))
	assert.True(t, os.IsNotExist(statErr), "memories/.git has no remote and must still be invalidated")

	warnings, err := workspace.ResidualWarnings(req)
	require.NoError(t, err)
	assert.Contains(t, warnings, "memories_v2/.git carries a remote and was left in place; its worktree contents were rewritten",
		"the warning must name memories_v2, not memories")
}

// TestMemoriesWorktreeGitBaselineWarningOmitsRewriteClauseWithZeroOccurrences
// guards against a false "worktree contents were rewritten" claim: a root
// whose remote-carrying baseline is left in place, but whose worktree never
// referenced the moved project at all, must get the shorter warning.
func TestMemoriesWorktreeGitBaselineWarningOmitsRewriteClauseWithZeroOccurrences(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	v2Root := filepath.Join(home.Dir, memoriesWorktreeV2Subdir)
	buildFixtureMemoriesGitBaseline(t, v2Root, fixtureGitConfigWithRemote)
	// Replace the fixture's default v2 content (which references
	// FixtureProjectPath()) with content about an unrelated project, so this
	// root carries zero occurrences of the moved project either way.
	require.NoError(t, os.RemoveAll(filepath.Join(v2Root, "rollout_summaries")))
	require.NoError(t, os.WriteFile(
		filepath.Join(v2Root, "memory_summary.md"), []byte("v1\n\nNotes about /Users/fixture/unrelated-project.\n"), 0o600,
	))

	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	planAndApply(t, workspace, req)

	assert.DirExists(t, filepath.Join(v2Root, gitDirName), "memories_v2/.git carries a remote and must be left in place")
	warnings, err := workspace.ResidualWarnings(req)
	require.NoError(t, err)
	assert.Contains(t, warnings, "memories_v2/.git carries a remote and was left in place",
		"a remote-carrying root with zero project occurrences must still be reported")
	assert.NotContains(t, warnings, "memories_v2/.git carries a remote and was left in place; its worktree contents were rewritten",
		"but must not falsely claim its worktree contents were rewritten")
}

// TestMemoriesWorktreeGitBaselineWarningReportsPendingRewriteBeforeApply
// guards the dry-run preview: ResidualWarnings runs before Apply, when the
// remote-carrying root's worktree still holds oldPath, so the warning must
// state the rewrite is still to come rather than claim it already happened.
func TestMemoriesWorktreeGitBaselineWarningReportsPendingRewriteBeforeApply(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	buildFixtureMemoriesGitBaseline(t, filepath.Join(home.Dir, memoriesWorktreeSubdir), fixtureGitConfigWithRemote)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}

	warnings, err := workspace.ResidualWarnings(req)

	require.NoError(t, err)
	assert.Contains(t, warnings, "memories/.git carries a remote and was left in place; its worktree contents are still to be rewritten")
	assert.NotContains(t, warnings, "memories/.git carries a remote and was left in place; its worktree contents were rewritten",
		"a dry run must not claim a rewrite that has not happened")
}

func TestMemoriesWorktreeGitBaselineWarningEmptyWhenSafeToDelete(t *testing.T) {
	root := t.TempDir()
	buildFixtureMemoriesGitBaseline(t, root, fixtureGitConfigNoRemote)

	warning, err := memoriesGitBaselineWarning(root, FixtureProjectPath(), "/Users/fixture/renamed-project")

	require.NoError(t, err)
	assert.Empty(t, warning)
}

func TestResidualWarningsReportsEraAAndGitBaselineLeftInPlace(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	// Force the git baseline into the "leave in place" shape so both
	// warning kinds are exercised by one ResidualWarnings call.
	buildFixtureMemoriesGitBaseline(t, filepath.Join(home.Dir, memoriesWorktreeSubdir), fixtureGitConfigWithRemote)

	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	warnings, err := workspace.ResidualWarnings(req)

	require.NoError(t, err)
	assert.Len(t, warnings, 3, "era-A, marketplace residual, and git-baseline-left-in-place warnings: %v", warnings)
}

// TestResidualWarningsReportsBackupWarningPerRoot guards that a stranded
// git-baseline backup under EACH memory worktree root is reported: the two
// roots keep independent rollback (memory_version.rs:16), so a leftover
// backup under one must not mask a leftover backup under the other.
func TestResidualWarningsReportsBackupWarningPerRoot(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	for _, subdir := range memoriesWorktreeSubdirs {
		require.NoError(t, os.MkdirAll(filepath.Join(home.Dir, subdir, gitDirName+gitBackupSuffix), 0o700))
	}

	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	warnings, err := workspace.ResidualWarnings(req)

	require.NoError(t, err)
	found := 0
	for _, warning := range warnings {
		if strings.Contains(warning, gitBackupSuffix) {
			found++
		}
	}
	assert.Equal(t, 2, found, "a stranded backup under each memory worktree root must be reported: %v", warnings)
}

// TestResidualWarnings_WarnsOnDivergentProfileSQLiteHome guards the wiring
// of profileSQLiteHomeWarning into the move command's existing warning
// channel: a profile overlay declaring a sqlite_home different from the
// resolved one must surface through the same ResidualWarnings call every
// other move residual reports through.
func TestResidualWarnings_WarnsOnDivergentProfileSQLiteHome(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.WriteFile(
		filepath.Join(home.Dir, "work.config.toml"),
		[]byte("sqlite_home = \""+elsewhere+"\"\n\n[projects.\""+FixtureProjectPath()+"\"]\ntrust_level = \"trusted\"\n"),
		0o600,
	))

	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	warnings, err := workspace.ResidualWarnings(req)

	require.NoError(t, err)
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning, "work.config.toml") {
			found = true
		}
	}
	assert.True(t, found, "a profile overlay declaring a different sqlite_home must be reported: %v", warnings)
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

// TestCodexDevWarningDetectsSymlinkAliasedValue guards finding H1 (spec
// §5.1) on the codex-dev.db surface: local_thread_catalog.cwd and
// automation_runs.source_cwd, like threads.cwd, can hold a symlink-aliased
// spelling of the project's cwd, since Codex records cwd verbatim and
// uncanonicalized everywhere. codexDevWarning never rewrites codex-dev.db
// (that contract is unchanged); it only detects and refuses. Before routing
// these two columns through canonical matching, a byte-literal comparison
// silently missed an aliased value, leaving a move free to proceed against
// a database that does reference the project.
func TestCodexDevWarningDetectsSymlinkAliasedValue(t *testing.T) {
	tempRoot := t.TempDir()
	realProject := filepath.Join(tempRoot, "real", "project")
	require.NoError(t, os.MkdirAll(realProject, 0o750))
	require.NoError(t, os.Symlink(filepath.Join(tempRoot, "real"), filepath.Join(tempRoot, "link")))
	aliasedCWD := filepath.Join(tempRoot, "link", "project")

	path := filepath.Join(tempRoot, "codex-dev.db")
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { _ = database.Close() }()
	_, err = database.ExecContext(context.Background(), `
		CREATE TABLE automations (cwds TEXT);
		CREATE TABLE automation_runs (source_cwd TEXT);
		CREATE TABLE local_thread_catalog (cwd TEXT NOT NULL);
	`)
	require.NoError(t, err)
	_, err = database.ExecContext(context.Background(), `INSERT INTO local_thread_catalog (cwd) VALUES (?)`, aliasedCWD)
	require.NoError(t, err)

	warning, err := codexDevWarning(path, realProject)

	require.NoError(t, err)
	assert.Equal(t, "codex-dev.db contains path references to the moved project and is never rewritten; refusing to move", warning)
}

// TestCodexDevWarningToleratesNullSourceCWD guards a NULL-handling
// regression from routing automation_runs.source_cwd through
// matchingColumnValues' canonical Go-side comparison instead of
// CountTextColumnRO's substring scan. automation_runs.source_cwd is
// NULLABLE in this package's own schema fixtures (unlike threads.cwd and
// local_thread_catalog.cwd, both NOT NULL); CountTextColumnRO's instr()
// predicate silently excludes a NULL row, but scanning that same row into a
// plain Go string errors instead, a hard failure on an ordinary
// codex-dev.db that happens to carry one alongside a genuinely matching row.
func TestCodexDevWarningToleratesNullSourceCWD(t *testing.T) {
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
	_, err = database.ExecContext(context.Background(), `INSERT INTO automation_runs (source_cwd) VALUES (NULL)`)
	require.NoError(t, err)
	_, err = database.ExecContext(context.Background(), `INSERT INTO automation_runs (source_cwd) VALUES (?)`, FixtureProjectPath())
	require.NoError(t, err)

	warning, err := codexDevWarning(path, FixtureProjectPath())

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
	plans, err := stateDBRewritePlansForProject(context.Background(), workspace.home.SQLiteDir, req.OldPath, req.NewPath)
	require.NoError(t, err)
	_, err = workspace.stateDBSurfaceWithPlans(req, pending, plans).Apply(context.Background(), undo)
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
	memoriesCount, countErr := countMemoriesDB(context.Background(), workspace.home.SQLiteDir, req.OldPath)
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
	plans, err := stateDBRewritePlansForProject(context.Background(), workspace.home.SQLiteDir, req.OldPath, req.NewPath)
	require.NoError(t, err)
	_, err = workspace.stateDBSurfaceWithPlans(req, pending, plans).Apply(context.Background(), undo)
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

func TestStateDBPlanFailsForMissingThreadsCwdColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	_, err = database.ExecContext(context.Background(), `CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT);`)
	require.NoError(t, err)

	_, err = matchingPathRewrites(context.Background(), path, FixtureProjectPath(), "/Users/fixture/renamed-project")

	require.EqualError(t, err, "required column threads.cwd is missing (observed columns: id, title)")
}

func TestFinalDatabaseSurfaceLeavesBackupAsWarningWhenCleanupFails(t *testing.T) {
	backup := filepath.Join(t.TempDir(), "git.cc-port-rollback.tmp")
	require.NoError(t, os.Mkdir(backup, 0o700))
	pending := &pendingMoveDatabases{gitBackup: []string{backup}, removeAll: func(string) error { return assert.AnError }}

	_, err := pending.commitSurface().Apply(context.Background(), tool.NewRestorer())

	require.NoError(t, err)
	assert.DirExists(t, backup)
	warning, err := gitBackupWarning(backup)
	require.NoError(t, err)
	assert.Contains(t, warning, backup)
}

// TestFinalDatabaseSurfaceAttemptsSecondBackupCleanupAfterFirstFails guards
// that a cleanup failure on one root's backup does not stop the commit
// surface from attempting the other root's backup.
func TestFinalDatabaseSurfaceAttemptsSecondBackupCleanupAfterFirstFails(t *testing.T) {
	backupV1 := filepath.Join(t.TempDir(), "memories-git.cc-port-rollback.tmp")
	backupV2 := filepath.Join(t.TempDir(), "memories-v2-git.cc-port-rollback.tmp")
	var attempted []string
	pending := &pendingMoveDatabases{
		gitBackup: []string{backupV1, backupV2},
		removeAll: func(path string) error {
			attempted = append(attempted, path)
			return assert.AnError
		},
	}

	_, err := pending.commitSurface().Apply(context.Background(), tool.NewRestorer())

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{backupV1, backupV2}, attempted,
		"cleanup must be attempted for every root's backup, not stop after the first failure")
}

func TestMemoriesWorktreeSurface_ReconcilesStrandedBackupBeforeRewrite(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	root := filepath.Join(home.Dir, memoriesWorktreeSubdir)
	backup := filepath.Join(root, gitDirName+gitBackupSuffix)
	require.NoError(t, os.Mkdir(backup, 0o700))
	pending := &pendingMoveDatabases{removeAll: os.RemoveAll}
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}

	_, err := workspace.memoriesWorktreeSurface(req, pending).Apply(context.Background(), tool.NewRestorer())

	require.NoError(t, err)
	_, err = pending.commitSurface().Apply(context.Background(), tool.NewRestorer())
	require.NoError(t, err)
	assert.NoDirExists(t, backup)
	assert.NoDirExists(t, filepath.Join(root, gitDirName))
}

func TestMemoriesWorktreeSurface_ReconcilesStrandedBackupWithoutWorktreeChanges(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	root := filepath.Join(home.Dir, memoriesWorktreeSubdir)
	backup := filepath.Join(root, gitDirName+gitBackupSuffix)
	require.NoError(t, os.Mkdir(backup, 0o700))
	pending := &pendingMoveDatabases{removeAll: os.RemoveAll}
	req := tool.MoveRequest{OldPath: "/Users/fixture/never-referenced", NewPath: "/Users/fixture/renamed-project"}

	count, err := workspace.memoriesWorktreeSurface(req, pending).Apply(context.Background(), tool.NewRestorer())

	require.NoError(t, err)
	assert.Zero(t, count.Count)
	assert.NoDirExists(t, backup)
	assert.DirExists(t, filepath.Join(root, gitDirName))
}

func TestMemoriesRewriteFailureRollsBackStateAndSurfacesRollbackErrors(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	undo := tool.NewRestorer()
	plans, err := stateDBRewritePlansForProject(
		context.Background(), workspace.home.SQLiteDir, FixtureProjectPath(), "/Users/fixture/renamed-project",
	)
	require.NoError(t, err)
	state, _, err := startStateDBRewritesWithPlan(
		context.Background(), workspace.home.SQLiteDir, FixtureProjectPath(), "/Users/fixture/renamed-project", plans, undo,
	)
	require.NoError(t, err)
	memoriesPaths, err := discoverDatabases(workspace.home.SQLiteDir, memoriesDBGlob)
	require.NoError(t, err)
	_, _, err = startDatabaseRewrites(
		context.Background(), memoriesPaths, FixtureProjectPath(), "/Users/fixture/renamed-project",
		func(_ context.Context, _ string, database *sqlrewrite.DB, _ *sqlrewrite.Tx, _, _ string) (int, error) {
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
	workspace := NewWorkspace(home, fakeGetenv(nil), noProcesses)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}

	surfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	for _, surface := range surfaces {
		if surface.Name != "agents-marketplace" {
			continue
		}
		count, err := surface.Plan(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 0, count.Count)
	}
}

func TestAgentsMarketplaceRewritesStructuredSourceWithDottedKeys(t *testing.T) {
	agentsDir := FixtureAgentsDir(t)
	marketplacePath := filepath.Join(agentsDir, agentsPluginsMarketplaceFile)
	contents := `{"plugins.with.dot":[{"source":{"source":"local","path":"` + FixtureProjectPath() + `/plugin"}}]}`
	require.NoError(t, os.WriteFile(marketplacePath, []byte(contents), 0o600))

	planned, err := planAgentsMarketplace(agentsDir, FixtureProjectPath())
	require.NoError(t, err)
	undo := tool.NewRestorer()
	applied, err := applyAgentsMarketplace(agentsDir, FixtureProjectPath(), "/Users/fixture/renamed-project", undo)
	require.NoError(t, err)
	undo.Cleanup()

	assert.Equal(t, 1, planned)
	assert.Equal(t, planned, applied)
	updated, err := os.ReadFile(marketplacePath) //nolint:gosec // test-controlled fixture path
	require.NoError(t, err)
	assert.Contains(t, string(updated), "/Users/fixture/renamed-project/plugin")
	assert.Contains(t, string(updated), `"source":"local"`)
}

func TestAgentsMarketplaceLeavesTagOnlySourceUntouched(t *testing.T) {
	agentsDir := FixtureAgentsDir(t)
	marketplacePath := filepath.Join(agentsDir, agentsPluginsMarketplaceFile)
	contents := `{"entries":[{"source":"local"}]}`
	require.NoError(t, os.WriteFile(marketplacePath, []byte(contents), 0o600))

	planned, err := planAgentsMarketplace(agentsDir, FixtureProjectPath())
	require.NoError(t, err)

	assert.Zero(t, planned)
}

func TestResidualAgentsWarningIncludesMarketplaceMisses(t *testing.T) {
	agentsDir := FixtureAgentsDir(t)
	marketplacePath := filepath.Join(agentsDir, agentsPluginsMarketplaceFile)
	contents := `{"entries":[{"source":"local","unrecognized_path":"` + FixtureProjectPath() + `/plugin"}]}`
	require.NoError(t, os.WriteFile(marketplacePath, []byte(contents), 0o600))

	warning, err := residualAgentsWarning(agentsDir, FixtureProjectPath())

	require.NoError(t, err)
	assert.NotEmpty(t, warning)
}
