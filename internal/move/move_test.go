package move_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func fixtureTargets(t *testing.T) []tool.Target {
	t.Helper()
	home := testutil.SetupFixture(t)
	return []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
}

type applyTestTool struct{}

func (*applyTestTool) Name() string                 { return "apply-test" }
func (*applyTestTool) DisplayName() string          { return "apply-test" }
func (*applyTestTool) Categories() []tool.Category  { return nil }
func (*applyTestTool) Detect() (bool, error)        { return true, nil }
func (*applyTestTool) ImplicitAnchorKeys() []string { return nil }
func (*applyTestTool) Open(string) (tool.Workspace, error) {
	return nil, errors.New("not exercised")
}

type applyTestWorkspace struct {
	lockPath          string
	surfaces          []tool.Surface
	warnings          []string
	warningErr        error
	warningSequence   [][]string
	warningErrs       []error
	residualCallCount int
}

func (*applyTestWorkspace) Root() string                                { return "/apply-test" }
func (workspace *applyTestWorkspace) LockPath() string                  { return workspace.lockPath }
func (*applyTestWorkspace) ActiveWriters() ([]tool.ActiveWriter, error) { return nil, nil }
func (workspace *applyTestWorkspace) MoveSurfaces(tool.MoveRequest) ([]tool.Surface, error) {
	return workspace.surfaces, nil
}
func (workspace *applyTestWorkspace) ResidualWarnings(tool.MoveRequest) ([]string, error) {
	if len(workspace.warningSequence) > 0 {
		index := workspace.residualCallCount
		workspace.residualCallCount++
		if index >= len(workspace.warningSequence) {
			index = len(workspace.warningSequence) - 1
		}
		var err error
		if index < len(workspace.warningErrs) {
			err = workspace.warningErrs[index]
		}
		return workspace.warningSequence[index], err
	}
	return workspace.warnings, workspace.warningErr
}
func (*applyTestWorkspace) Placeholders(string, map[string]bool) ([]manifest.Placeholder, error) {
	return nil, errors.New("not exercised")
}
func (*applyTestWorkspace) Export(context.Context, string, map[string]bool, *archive.Sink) (tool.ExportResult, error) {
	return tool.ExportResult{}, errors.New("not exercised")
}
func (*applyTestWorkspace) PreflightDirs(string) []string { return nil }
func (*applyTestWorkspace) ImplicitAnchors(string) (map[string]string, error) {
	return nil, errors.New("not exercised")
}
func (*applyTestWorkspace) Stage(context.Context, string, archive.Entry, map[string]string) ([]archive.Staged, error) {
	return nil, errors.New("not exercised")
}
func (*applyTestWorkspace) Finalize(context.Context, string, *archive.StagedSet) ([]string, error) {
	return nil, errors.New("not exercised")
}
func (*applyTestWorkspace) ReferenceSurfaces(context.Context, string) ([]tool.CountSurface, error) {
	return nil, errors.New("not exercised")
}
func (*applyTestWorkspace) DiskCategories(context.Context, string) ([]tool.SizeCategory, error) {
	return nil, errors.New("not exercised")
}
func (*applyTestWorkspace) EnumerateProjects(context.Context) ([]tool.ProjectInfo, error) {
	return nil, errors.New("not exercised")
}

func applyTestTarget(t *testing.T, workspace *applyTestWorkspace) []tool.Target {
	t.Helper()
	workspace.lockPath = filepath.Join(t.TempDir(), ".cc-port.lock")
	return []tool.Target{{Tool: &applyTestTool{}, Workspace: workspace}}
}

func TestDryRun_ReportsSurfaceCountsPerTool(t *testing.T) {
	targets := fixtureTargets(t)
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"

	plan, err := move.DryRun(context.Background(), targets, move.Options{OldPath: oldPath, NewPath: newPath})
	require.NoError(t, err)
	require.Len(t, plan.ByTool, 1)

	claudePlan := plan.ByTool[0]
	assert.Equal(t, "claude", claudePlan.Tool)
	assert.False(t, claudePlan.Absent)
	assert.NotEmpty(t, claudePlan.Surfaces)

	total := 0
	for _, surface := range claudePlan.Surfaces {
		total += surface.Count
	}
	assert.Positive(t, total, "the fixture project must have at least one rewritable reference")
}

func TestDryRun_ReportsMalformedHistoryLineWarning(t *testing.T) {
	home := testutil.SetupFixture(t)
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"
	records := []byte(`{"project":"` + oldPath + `"}` + "\nnot json\n" + `{"project":"` + oldPath + `"}` + "\n")
	require.NoError(t, os.WriteFile(home.HistoryFile(), records, 0o600))
	targets := []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}

	plan, err := move.DryRun(t.Context(), targets, move.Options{OldPath: oldPath, NewPath: newPath, RefsOnly: true})

	require.NoError(t, err)
	assert.Contains(t, plan.ByTool[0].Warnings, "history.jsonl: 1 malformed line(s) skipped (line 2)")
}

func TestApply_ReportsMalformedHistoryLineWarning(t *testing.T) {
	home := testutil.SetupFixture(t)
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"
	records := []byte(`{"project":"` + oldPath + `"}` + "\nnot json\n" + `{"project":"` + oldPath + `"}` + "\n")
	require.NoError(t, os.WriteFile(home.HistoryFile(), records, 0o600))
	targets := []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}

	result, err := move.Apply(t.Context(), targets, move.Options{OldPath: oldPath, NewPath: newPath, RefsOnly: true})

	require.NoError(t, err)
	assert.Contains(t, result.ByTool[0].Warnings, "history.jsonl: 1 malformed line(s) skipped (line 2)")
}

func TestSessionsSurface_ReportsMalformedSessionFileWithoutChangingItsBytes(t *testing.T) {
	home := testutil.SetupFixture(t)
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"
	malformedPath := filepath.Join(home.SessionsDir(), "malformed-session.json")
	original := []byte(`{"cwd":`)
	require.NoError(t, os.WriteFile(malformedPath, original, 0o600))
	workspace := claude.NewWorkspace(home)
	surfaces, err := workspace.MoveSurfaces(tool.MoveRequest{OldPath: oldPath, NewPath: newPath, RefsOnly: true})
	require.NoError(t, err)
	var sessions tool.Surface
	for _, surface := range surfaces {
		if surface.Name == "sessions" {
			sessions = surface
			break
		}
	}
	require.NotNil(t, sessions.Apply)

	result, err := sessions.Apply(t.Context(), tool.NewRestorer())

	require.NoError(t, err)
	assert.Contains(t, result.Warnings, "sessions/malformed-session.json: malformed JSON preserved unchanged")
	actual, readErr := os.ReadFile(malformedPath) //nolint:gosec // G304: path from t.TempDir fixture
	require.NoError(t, readErr)
	assert.Equal(t, original, actual)
}

func TestDryRun_AbsentProjectReportsZeroSurfaces(t *testing.T) {
	targets := fixtureTargets(t)

	plan, err := move.DryRun(context.Background(), targets, move.Options{
		OldPath: "/no/such/project", NewPath: "/no/such/project-renamed",
	})
	require.NoError(t, err, "a project unknown to every target must not fail the dry run")
	require.Len(t, plan.ByTool, 1)
	assert.True(t, plan.ByTool[0].Absent)
	assert.Empty(t, plan.ByTool[0].Surfaces)
}

func TestApply_MovesProjectAndUpdatesReferences(t *testing.T) {
	targets := fixtureTargets(t)
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"

	claudeWorkspace := targets[0].Workspace
	home := claudeWorkspaceHome(t, claudeWorkspace)

	// RefsOnly: true — the fixture project path is a symbolic path under
	// /Users/test, not a real directory this process can create; refs-only
	// mode exercises the reference rewrite without touching it on disk.
	result, err := move.Apply(context.Background(), targets, move.Options{OldPath: oldPath, NewPath: newPath, RefsOnly: true})
	require.NoError(t, err)
	require.False(t, result.Failed(), "apply must succeed against a fresh fixture")

	oldEncodedDir := home.ProjectDir(oldPath)
	newEncodedDir := home.ProjectDir(newPath)

	_, statErr := os.Stat(oldEncodedDir)
	assert.True(t, os.IsNotExist(statErr), "old encoded project dir must be gone after apply")
	_, statErr = os.Stat(newEncodedDir)
	require.NoError(t, statErr, "new encoded project dir must exist after apply")

	historyBytes, err := os.ReadFile(home.HistoryFile())
	require.NoError(t, err)
	assert.Contains(t, string(historyBytes), newPath)
	require.NotEmpty(t, result.ByTool[0].Warnings)
	warnings := strings.Join(result.ByTool[0].Warnings, "\n")
	assert.Contains(t, warnings, "file-history snapshot(s) preserved verbatim; bodies may still contain the old project path")
}

func TestClaudeMoveSurfaces_AllowsPhysicalDestinationWhenSourceAlreadyGone(t *testing.T) {
	targets := fixtureTargets(t)
	workspace := targets[0].Workspace
	oldPath := testutil.FixtureProjectPath()

	_, err := workspace.MoveSurfaces(tool.MoveRequest{OldPath: oldPath, NewPath: t.TempDir()})

	require.NoError(t, err)
}

func TestClaudeMoveSurfaces_RefsOnlyIgnoresPhysicalDestination(t *testing.T) {
	targets := fixtureTargets(t)
	workspace := targets[0].Workspace

	_, err := workspace.MoveSurfaces(tool.MoveRequest{OldPath: testutil.FixtureProjectPath(), NewPath: t.TempDir(), RefsOnly: true})

	require.NoError(t, err)
}

func TestClaudeMoveApply_RemovesStagingDirectoryBeforePromoting(t *testing.T) {
	targets := fixtureTargets(t)
	workspace := targets[0].Workspace
	home := claudeWorkspaceHome(t, workspace)
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"
	staging := home.ProjectDir(newPath) + ".cc-port-staging.tmp"
	require.NoError(t, os.MkdirAll(staging, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(staging, "partial"), []byte("partial"), 0o600))

	result, err := move.Apply(context.Background(), targets, move.Options{OldPath: oldPath, NewPath: newPath, RefsOnly: true})

	require.NoError(t, err)
	require.False(t, result.Failed())
	assert.NoDirExists(t, staging)
	assert.DirExists(t, home.ProjectDir(newPath))
}

func TestClaudeMoveApply_ResumesExistingEncodedDestination(t *testing.T) {
	targets := fixtureTargets(t)
	workspace := targets[0].Workspace
	home := claudeWorkspaceHome(t, workspace)
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"
	oldEncodedDir := home.ProjectDir(oldPath)
	newEncodedDir := home.ProjectDir(newPath)
	require.NoError(t, os.MkdirAll(newEncodedDir, 0o750))
	require.NoError(t, os.WriteFile(newEncodedDir+rewrite.MarkerSuffix, []byte(oldEncodedDir), 0o600))

	result, err := move.Apply(context.Background(), targets, move.Options{OldPath: oldPath, NewPath: newPath, RefsOnly: true})

	require.NoError(t, err)
	require.False(t, result.Failed())
	assert.NoDirExists(t, oldEncodedDir)
	assert.DirExists(t, newEncodedDir)
	assert.NoFileExists(t, newEncodedDir+rewrite.MarkerSuffix)
}

func TestClaudeMoveApply_RefusesForeignEncodedDestinationAndPreservesSource(t *testing.T) {
	targets := fixtureTargets(t)
	workspace := targets[0].Workspace
	home := claudeWorkspaceHome(t, workspace)
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"
	oldEncodedDir := home.ProjectDir(oldPath)
	newEncodedDir := home.ProjectDir(newPath)
	sourceFile := filepath.Join(oldEncodedDir, "source-only.txt")

	require.NoError(t, os.WriteFile(sourceFile, []byte("source data"), 0o600))
	require.NoError(t, os.MkdirAll(newEncodedDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(newEncodedDir, "unrelated.txt"), []byte("foreign data"), 0o600))

	_, err := move.Apply(context.Background(), targets, move.Options{OldPath: oldPath, NewPath: newPath, RefsOnly: true})

	require.Error(t, err, "an unmarked existing destination must be refused")
	require.ErrorContains(t, err, "new project directory already exists")
	data, readErr := os.ReadFile(sourceFile) //nolint:gosec // G304: test fixture path
	require.NoError(t, readErr, "the refused collision must leave the source untouched")
	assert.Equal(t, "source data", string(data))
	assert.FileExists(t, filepath.Join(newEncodedDir, "unrelated.txt"))
}

func TestClaudeMoveApply_RefusesStaleMarkedForeignEncodedDestinationAndPreservesSource(t *testing.T) {
	home := testutil.SetupFixture(t)
	fixedNow := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	workspace := claude.NewWorkspaceForTest(home, os.Getenv, func(int) bool { return false }, func() time.Time { return fixedNow })
	targets := []tool.Target{{Tool: claude.New(), Workspace: workspace}}
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"
	oldEncodedDir := home.ProjectDir(oldPath)
	newEncodedDir := home.ProjectDir(newPath)
	sourceFile := filepath.Join(oldEncodedDir, "source-only.txt")
	markerPath := newEncodedDir + rewrite.MarkerSuffix

	require.NoError(t, os.WriteFile(sourceFile, []byte("source data"), 0o600))
	require.NoError(t, os.MkdirAll(newEncodedDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(newEncodedDir, "unrelated.txt"), []byte("foreign data"), 0o600))
	require.NoError(t, os.WriteFile(markerPath, []byte(oldEncodedDir), 0o600))
	staleAt := fixedNow.Add(-rewrite.MarkerFreshnessWindow - time.Second)
	require.NoError(t, os.Chtimes(markerPath, staleAt, staleAt))

	_, err := move.Apply(context.Background(), targets, move.Options{OldPath: oldPath, NewPath: newPath, RefsOnly: true})

	require.Error(t, err, "a stale marker must not make a foreign destination resumable")
	require.ErrorContains(t, err, "new project directory already exists")
	data, readErr := os.ReadFile(sourceFile) //nolint:gosec // G304: test fixture path
	require.NoError(t, readErr, "the refused collision must leave the source untouched")
	assert.Equal(t, "source data", string(data))
	assert.FileExists(t, filepath.Join(newEncodedDir, "unrelated.txt"))
}

func TestApply_CancellationRestoresCompletedSurfaces(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restored := false
	workspace := &applyTestWorkspace{surfaces: []tool.Surface{
		{
			Name: "first",
			Plan: func(context.Context) (tool.SurfaceResult, error) { return tool.SurfaceResult{}, nil },
			Apply: func(context.Context, *tool.Restorer) (tool.SurfaceResult, error) {
				return tool.SurfaceResult{}, nil
			},
		},
		{
			Name:  "second",
			Plan:  func(context.Context) (tool.SurfaceResult, error) { return tool.SurfaceResult{}, nil },
			Apply: func(context.Context, *tool.Restorer) (tool.SurfaceResult, error) { return tool.SurfaceResult{}, nil },
		},
	}}
	workspace.surfaces[0].Apply = func(_ context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
		undo.RegisterUndo(func() error { restored = true; return nil })
		cancel()
		return tool.SurfaceResult{Count: 1}, nil
	}

	result, err := move.Apply(ctx, applyTestTarget(t, workspace), move.Options{OldPath: "/old", NewPath: "/new"})

	require.NoError(t, err)
	require.True(t, restored)
	require.True(t, result.Failed())
	require.ErrorIs(t, result.ByTool[0].Err, context.Canceled)
}

func TestApply_CarriesResidualWarnings(t *testing.T) {
	workspace := &applyTestWorkspace{warnings: []string{"left untouched by design"}}

	result, err := move.Apply(context.Background(), applyTestTarget(t, workspace), move.Options{OldPath: "/old", NewPath: "/new"})

	require.NoError(t, err)
	require.True(t, result.ByTool[0].Success)
	assert.Equal(t, []string{"left untouched by design"}, result.ByTool[0].Warnings)
}

func TestApply_ReportsResidualWarningInspectionFailureNonFatally(t *testing.T) {
	workspace := &applyTestWorkspace{warningErr: assert.AnError}

	result, err := move.Apply(context.Background(), applyTestTarget(t, workspace), move.Options{OldPath: "/old", NewPath: "/new"})

	require.NoError(t, err)
	require.True(t, result.ByTool[0].Success)
	assert.Contains(t, result.ByTool[0].Warnings, "could not inspect residual warnings: assert.AnError general error for testing")
}

func TestApply_KeepsResidualWarningsWhenInspectionAlsoFails(t *testing.T) {
	workspace := &applyTestWorkspace{warnings: []string{"unrewritten snapshot"}, warningErr: assert.AnError}

	result, err := move.Apply(context.Background(), applyTestTarget(t, workspace), move.Options{OldPath: "/old", NewPath: "/new"})

	require.NoError(t, err)
	require.True(t, result.ByTool[0].Success)
	assert.Contains(t, result.ByTool[0].Warnings, "unrewritten snapshot")
	assert.Contains(t, result.ByTool[0].Warnings, "could not inspect residual warnings: assert.AnError general error for testing")
}

func TestApply_UsesOnlyPostApplyResidualWarnings(t *testing.T) {
	workspace := &applyTestWorkspace{warningSequence: [][]string{{"pre-apply residual"}, {"checkpoint after apply"}}}

	result, err := move.Apply(context.Background(), applyTestTarget(t, workspace), move.Options{OldPath: "/old", NewPath: "/new"})

	require.NoError(t, err)
	assert.Equal(t, []string{"checkpoint after apply"}, result.ByTool[0].Warnings)
}

func TestDryRunAndApply_WarnWhenNoSelectedToolMovesProjectDirectory(t *testing.T) {
	workspace := &applyTestWorkspace{}
	targets := applyTestTarget(t, workspace)

	plan, err := move.DryRun(context.Background(), targets, move.Options{OldPath: "/old", NewPath: "/new"})
	require.NoError(t, err)
	assert.Contains(t, plan.Warnings, move.NoPhysicalMoveWarning)

	result, err := move.Apply(context.Background(), targets, move.Options{OldPath: "/old", NewPath: "/new"})
	require.NoError(t, err)
	assert.Contains(t, result.Warnings, move.NoPhysicalMoveWarning)
}

// claudeWorkspaceHome extracts the underlying *claude.Home for assertions
// that need direct filesystem access. Uses the same fixture Workspace the
// test already built rather than re-deriving a second one.
func claudeWorkspaceHome(t *testing.T, workspace tool.Workspace) *claude.Home {
	t.Helper()
	return &claude.Home{Dir: workspace.Root(), ConfigFile: workspace.Root() + ".json"}
}
