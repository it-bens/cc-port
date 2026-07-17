package move_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func fixtureTargets(t *testing.T) []tool.Target {
	t.Helper()
	home := testutil.SetupFixture(t)
	return []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
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
}

// claudeWorkspaceHome extracts the underlying *claude.Home for assertions
// that need direct filesystem access. Uses the same fixture Workspace the
// test already built rather than re-deriving a second one.
func claudeWorkspaceHome(t *testing.T, workspace tool.Workspace) *claude.Home {
	t.Helper()
	return &claude.Home{Dir: workspace.Root(), ConfigFile: workspace.Root() + ".json"}
}
