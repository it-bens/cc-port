package move_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/tool/claude/claudetest"
	"github.com/it-bens/cc-port/internal/tool/codex"
	"github.com/it-bens/cc-port/internal/tool/codex/codextest"
)

// nestingOperations runs the nesting precondition through both move entry points.
var nestingOperations = []struct {
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

// TestNestedMovePrecondition_RefusesNestedGeometries passes no targets: the
// nesting precondition reads only the options and runs before any target is
// enumerated, so a refused geometry needs no staged tool home.
func TestNestedMovePrecondition_RefusesNestedGeometries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		newPath  string
		refsOnly bool
	}{
		{name: "equal paths", newPath: "/old"},
		{name: "boundary descendant", newPath: "/old/nested"},
		{name: "refs only descendant", newPath: "/old/nested", refsOnly: true},
	}

	for _, test := range tests {
		for _, operation := range nestingOperations {
			t.Run(test.name+"/"+operation.name, func(t *testing.T) {
				t.Parallel()
				err := operation.run(t.Context(), nil, move.Options{
					OldPath:  "/old",
					NewPath:  test.newPath,
					RefsOnly: test.refsOnly,
				})

				require.ErrorIs(t, err, move.ErrNestedMove)
			})
		}
	}
}

func TestNestedMovePrecondition_AllowsSafeSibling(t *testing.T) {
	t.Parallel()
	targets := []struct {
		name  string
		setup func(*testing.T) (tool.Target, string)
	}{
		{
			name: "claude",
			setup: func(t *testing.T) (tool.Target, string) {
				home := claudetest.SetupFixture(t)
				return tool.Target{Tool: claude.New(), Workspace: claudetest.NewWorkspace(home)}, claudetest.FixtureProjectPath()
			},
		},
		{
			name: "codex",
			setup: func(t *testing.T) (tool.Target, string) {
				codexDir := codextest.SetupFixture(t)
				home := &codex.Home{Dir: codexDir, SQLiteDir: codexDir}
				return tool.Target{Tool: codex.New(), Workspace: quietCodexWorkspace(home)}, codextest.FixtureProjectPath()
			},
		},
	}
	for _, targetCase := range targets {
		for _, operation := range nestingOperations {
			t.Run(targetCase.name+"/"+operation.name, func(t *testing.T) {
				t.Parallel()
				target, oldPath := targetCase.setup(t)

				err := operation.run(t.Context(), []tool.Target{target}, move.Options{
					OldPath: oldPath,
					NewPath: oldPath + "-backup",
				})

				assert.NotErrorIs(t, err, move.ErrNestedMove)
			})
		}
	}
}

// TestMove_RefusesStagingCollision covers the pathological geometry where a
// move's source path is identical to the destination's staging sibling
// (OldPath == NewPath+StagingSuffix). Reconciling that staging path before
// promotion would delete OldPath itself — the real source, not foreign
// debris — so this precondition must refuse before any target is touched;
// no tool.Target is needed since the check runs before target enumeration.
func TestMove_RefusesStagingCollision(t *testing.T) {
	t.Parallel()
	newPath := filepath.Join(t.TempDir(), "project")
	oldPath := newPath + rewrite.StagingSuffix
	require.NoError(t, os.MkdirAll(oldPath, 0o750))

	_, dryRunErr := move.DryRun(t.Context(), nil, move.Options{OldPath: oldPath, NewPath: newPath})
	require.ErrorIs(t, dryRunErr, move.ErrStagingCollision)
	assert.DirExists(t, oldPath, "a refused dry run must not touch the source directory")

	_, applyErr := move.Apply(t.Context(), nil, move.Options{OldPath: oldPath, NewPath: newPath})
	require.ErrorIs(t, applyErr, move.ErrStagingCollision)
	assert.DirExists(t, oldPath, "a refused apply must not delete the source directory")
}

// TestMove_RefusesRootNestedMove covers the root-parent boundary case:
// IsBoundaryDescendant("/", "/anything") must report true, so a move whose
// old path is "/" refuses a new path nested under it exactly like any other
// nested move — no tool.Target is needed since the precondition check runs
// before target enumeration.
func TestMove_RefusesRootNestedMove(t *testing.T) {
	t.Parallel()
	_, dryRunErr := move.DryRun(t.Context(), nil, move.Options{OldPath: "/", NewPath: "/.cc-port-new"})
	require.ErrorIs(t, dryRunErr, move.ErrNestedMove)

	_, applyErr := move.Apply(t.Context(), nil, move.Options{OldPath: "/", NewPath: "/.cc-port-new"})
	require.ErrorIs(t, applyErr, move.ErrNestedMove)
}

func TestApply_RejectedNestedMoveLeavesClaudeStateUnchangedAcrossRetries(t *testing.T) {
	t.Parallel()
	home := claudetest.SetupFixture(t)
	targets := []tool.Target{{Tool: claude.New(), Workspace: claudetest.NewWorkspace(home)}}
	oldPath := claudetest.FixtureProjectPath()
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
	return codex.NewWorkspace(
		home,
		func(string) string { return "" },
		func() ([]codex.ProcessInfo, error) { return nil, nil },
	)
}
