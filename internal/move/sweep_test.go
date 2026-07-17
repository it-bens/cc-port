package move_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// fakeTool and fakeWorkspace are a minimal, self-contained tool.Tool /
// tool.Workspace pair used only to verify move's multi-target handling —
// they never touch a real adapter package.
type fakeTool struct{ name string }

func (f *fakeTool) Name() string                 { return f.name }
func (f *fakeTool) DisplayName() string          { return f.name }
func (f *fakeTool) Categories() []tool.Category  { return nil }
func (f *fakeTool) Detect() (bool, error)        { return true, nil }
func (f *fakeTool) ImplicitAnchorKeys() []string { return nil }
func (f *fakeTool) Open(string) (tool.Workspace, error) {
	return nil, errors.New("fakeTool.Open is not exercised by these tests")
}

type fakeWorkspace struct {
	moveErr  error
	lockPath string
}

func (w *fakeWorkspace) Root() string                                { return "/fake" }
func (w *fakeWorkspace) LockPath() string                            { return w.lockPath }
func (w *fakeWorkspace) ActiveWriters() ([]tool.ActiveWriter, error) { return nil, nil }

func (w *fakeWorkspace) MoveSurfaces(tool.MoveRequest) ([]tool.Surface, error) {
	if w.moveErr != nil {
		return nil, w.moveErr
	}
	return nil, nil
}

func (w *fakeWorkspace) ResidualWarnings(tool.MoveRequest) ([]string, error) { return nil, nil }

func (w *fakeWorkspace) Placeholders(string, map[string]bool) ([]manifest.Placeholder, error) {
	return nil, errors.New("not exercised")
}

func (w *fakeWorkspace) Export(context.Context, string, map[string]bool, *archive.Sink) (tool.ExportResult, error) {
	return tool.ExportResult{}, errors.New("not exercised")
}

func (w *fakeWorkspace) PreflightDirs(string) []string { return nil }

func (w *fakeWorkspace) ImplicitAnchors(string) (map[string]string, error) {
	return nil, errors.New("not exercised")
}

func (w *fakeWorkspace) Stage(context.Context, string, archive.Entry, map[string]string) ([]archive.Staged, error) {
	return nil, errors.New("not exercised")
}

func (w *fakeWorkspace) Finalize(context.Context, string, *archive.StagedSet) ([]string, error) {
	return nil, errors.New("not exercised")
}

func (w *fakeWorkspace) ReferenceSurfaces(string) ([]tool.CountSurface, error) {
	return nil, errors.New("not exercised")
}

func (w *fakeWorkspace) DiskCategories(string) ([]tool.SizeCategory, error) {
	return nil, errors.New("not exercised")
}

func (w *fakeWorkspace) EnumerateProjects() ([]tool.ProjectInfo, error) {
	return nil, errors.New("not exercised")
}

var (
	_ tool.Tool      = (*fakeTool)(nil)
	_ tool.Workspace = (*fakeWorkspace)(nil)
)

// TestDryRun_MultiTargetIndependence verifies that internal/move needs no
// special-casing to handle a target the current run does not fully know
// about, alongside one it does: each target's Plan/Apply outcome is
// entirely a function of the []tool.Target slice the command layer
// hands in. This is also the mechanism a swept-out (tool.ErrToolAbsent)
// tool relies on: since that tool is simply never added to the []tool.Target
// slice in the first place (a decision made where Workspaces are opened,
// out of this bundle's fence — see the report accompanying this bundle),
// nothing here needs to change to support it; the same "operate purely on
// the given slice" property that this test proves already covers it.
func TestDryRun_MultiTargetIndependence(t *testing.T) {
	home := testutil.SetupFixture(t)
	targets := []tool.Target{
		{Tool: claude.New(), Workspace: claude.NewWorkspace(home)},
		{Tool: &fakeTool{name: "fake"}, Workspace: &fakeWorkspace{moveErr: tool.ErrProjectAbsent, lockPath: filepath.Join(t.TempDir(), ".cc-port.lock")}},
	}
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"

	plan, err := move.DryRun(context.Background(), targets, move.Options{OldPath: oldPath, NewPath: newPath})

	require.NoError(t, err)
	require.Len(t, plan.ByTool, 2)

	claudePlan := plan.ByTool[0]
	assert.Equal(t, "claude", claudePlan.Tool)
	assert.False(t, claudePlan.Absent)
	assert.NotEmpty(t, claudePlan.Surfaces)

	fakePlan := plan.ByTool[1]
	assert.Equal(t, "fake", fakePlan.Tool)
	assert.True(t, fakePlan.Absent, "a target reporting ErrProjectAbsent must not affect any other target's plan")
	assert.Empty(t, fakePlan.Surfaces)
}

// TestApply_MultiTargetIndependence is Apply's counterpart: a target that
// reports ErrProjectAbsent is skipped (Absent: true, Success left false but
// not counted as a failure by Failed()) while the real target still
// applies normally.
func TestApply_MultiTargetIndependence(t *testing.T) {
	home := testutil.SetupFixture(t)
	targets := []tool.Target{
		{Tool: claude.New(), Workspace: claude.NewWorkspace(home)},
		{Tool: &fakeTool{name: "fake"}, Workspace: &fakeWorkspace{moveErr: tool.ErrProjectAbsent, lockPath: filepath.Join(t.TempDir(), ".cc-port.lock")}},
	}
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"

	result, err := move.Apply(context.Background(), targets, move.Options{OldPath: oldPath, NewPath: newPath, RefsOnly: true})

	require.NoError(t, err)
	require.False(t, result.Failed(), "an absent target must not be reported as a failure")
	require.Len(t, result.ByTool, 2)
	assert.True(t, result.ByTool[0].Success)
	assert.True(t, result.ByTool[1].Absent)
}
