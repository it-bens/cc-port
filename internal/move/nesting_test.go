package move_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/tool/codex"
)

func TestNestedMovePrecondition(t *testing.T) {
	tests := []struct {
		name     string
		newPath  func(string) string
		refsOnly bool
		refused  bool
	}{
		{name: "equal paths", newPath: func(oldPath string) string { return oldPath }, refused: true},
		{name: "boundary descendant", newPath: func(oldPath string) string { return oldPath + "/nested" }, refused: true},
		{name: "safe sibling", newPath: func(oldPath string) string { return oldPath + "-backup" }},
		{name: "refs only descendant", newPath: func(oldPath string) string { return oldPath + "/nested" }, refsOnly: true, refused: true},
	}
	targets := []struct {
		name  string
		setup func(*testing.T) (tool.Target, string)
	}{
		{
			name: "claude",
			setup: func(t *testing.T) (tool.Target, string) {
				home := testutil.SetupFixture(t)
				return tool.Target{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}, testutil.FixtureProjectPath()
			},
		},
		{
			name: "codex",
			setup: func(t *testing.T) (tool.Target, string) {
				home := codex.SetupFixture(t)
				return tool.Target{Tool: codex.New(), Workspace: quietCodexWorkspace(home)}, codex.FixtureProjectPath()
			},
		},
	}
	operations := []struct {
		name string
		run  func(context.Context, []tool.Target, move.Options) error
	}{
		{
			name: "dry run",
			run: func(ctx context.Context, targets []tool.Target, options move.Options) error {
				_, err := move.DryRun(ctx, targets, options)
				return err
			},
		},
		{
			name: "apply",
			run: func(ctx context.Context, targets []tool.Target, options move.Options) error {
				_, err := move.Apply(ctx, targets, options)
				return err
			},
		},
	}

	for _, test := range tests {
		for _, targetCase := range targets {
			for _, operation := range operations {
				t.Run(test.name+"/"+targetCase.name+"/"+operation.name, func(t *testing.T) {
					target, oldPath := targetCase.setup(t)
					err := operation.run(t.Context(), []tool.Target{target}, move.Options{
						OldPath:  oldPath,
						NewPath:  test.newPath(oldPath),
						RefsOnly: test.refsOnly,
					})

					if test.refused {
						require.Error(t, err)
						assert.ErrorIs(t, err, move.ErrNestedMove)
						return
					}
					assert.NotErrorIs(t, err, move.ErrNestedMove)
				})
			}
		}
	}
}

func TestApply_RejectedNestedMoveLeavesClaudeStateUnchangedAcrossRetries(t *testing.T) {
	home := testutil.SetupFixture(t)
	targets := []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
	oldPath := testutil.FixtureProjectPath()
	before, err := os.ReadFile(home.HistoryFile())
	require.NoError(t, err)

	for attempt := range 2 {
		_, err := move.Apply(t.Context(), targets, move.Options{OldPath: oldPath, NewPath: oldPath + "/nested", RefsOnly: true})
		require.Error(t, err, "attempt %d", attempt+1)
		require.ErrorIs(t, err, move.ErrNestedMove, "attempt %d", attempt+1)

		after, readErr := os.ReadFile(home.HistoryFile())
		require.NoError(t, readErr)
		assert.Equal(t, before, after)
	}
}

func quietCodexWorkspace(home *codex.Home) *codex.Workspace {
	return codex.NewWorkspaceForTest(
		home,
		func(string) string { return "" },
		func() ([]codex.ProcessInfo, error) { return nil, nil },
		func() time.Time { return time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC) },
		func(int) bool { return false },
	)
}
