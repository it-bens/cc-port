package claude_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func TestMoveSurfaces_DryRunAndApplyCountsMatch(t *testing.T) {
	home := testutil.SetupFixture(t)
	workspace := claude.NewWorkspace(home)
	req := tool.MoveRequest{OldPath: testutil.FixtureProjectPath(), NewPath: testutil.FixtureProjectPath() + "-renamed", RefsOnly: true}

	planned := surfaceCounts(t, workspace, req, false)
	applied := surfaceCounts(t, workspace, req, true)

	assert.Equal(t, planned, applied)
}

func TestMoveSurfaces_SecondApplyReportsProjectAbsent(t *testing.T) {
	home := testutil.SetupFixture(t)
	workspace := claude.NewWorkspace(home)
	req := tool.MoveRequest{OldPath: testutil.FixtureProjectPath(), NewPath: testutil.FixtureProjectPath() + "-renamed", RefsOnly: true}

	_ = surfaceCounts(t, workspace, req, true)
	_, err := workspace.MoveSurfaces(req)

	require.ErrorIs(t, err, tool.ErrProjectAbsent)
}

func surfaceCounts(t *testing.T, workspace *claude.Workspace, req tool.MoveRequest, apply bool) map[string]int {
	t.Helper()
	surfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	counts := make(map[string]int, len(surfaces))
	undo := tool.NewRestorer()
	for _, surface := range surfaces {
		if apply {
			count, applyErr := surface.Apply(context.Background(), undo)
			require.NoError(t, applyErr)
			counts[surface.Name] = count.Count
			continue
		}
		count, planErr := surface.Plan(context.Background())
		require.NoError(t, planErr)
		counts[surface.Name] = count.Count
	}
	if !apply {
		return counts
	}
	undo.Cleanup()
	return counts
}
