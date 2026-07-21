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

// TestMoveSurfaces_DryRunAndApplyCountsMatch guards Plan/Apply count parity
// for both a fresh move and a move resumed after a crash mid-apply
// (finding A1): a SIGKILL after the sessions surface has already rewritten
// every session witness to NewPath, but before the encoded project
// directory itself is promoted, must not desync what a re-run's dry-run
// reports from what its apply actually does.
func TestMoveSurfaces_DryRunAndApplyCountsMatch(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(t *testing.T, workspace *claude.Workspace, req tool.MoveRequest)
	}{
		{name: "fresh", arrange: func(*testing.T, *claude.Workspace, tool.MoveRequest) {}},
		{
			name: "resume after witness flip",
			arrange: func(t *testing.T, workspace *claude.Workspace, req tool.MoveRequest) {
				t.Helper()
				// Simulate a SIGKILL right after the sessions surface commits:
				// apply ONLY that one surface, leaving the encoded project
				// directory itself untouched.
				preflightSurfaces, err := workspace.MoveSurfaces(req)
				require.NoError(t, err)
				undo := tool.NewRestorer()
				applied := false
				for _, surface := range preflightSurfaces {
					if surface.Name != "sessions" {
						continue
					}
					result, err := surface.Apply(context.Background(), undo)
					require.NoError(t, err)
					require.Positive(t, result.Count, "sanity: the simulated crash point must have rewritten a witness")
					applied = true
				}
				require.True(t, applied, "sanity: MoveSurfaces must include the sessions surface")
				undo.Cleanup()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := testutil.SetupFixture(t)
			workspace := claude.NewWorkspace(home)
			req := tool.MoveRequest{OldPath: testutil.FixtureProjectPath(), NewPath: testutil.FixtureProjectPath() + "-renamed", RefsOnly: true}
			test.arrange(t, workspace, req)

			planned := surfaceCounts(t, workspace, req, false)
			applied := surfaceCounts(t, workspace, req, true)

			assert.Equal(t, planned, applied)
		})
	}
}

func TestMoveSurfaces_SecondApplyReportsProjectAbsent(t *testing.T) {
	home := testutil.SetupFixture(t)
	workspace := claude.NewWorkspace(home)
	req := tool.MoveRequest{OldPath: testutil.FixtureProjectPath(), NewPath: testutil.FixtureProjectPath() + "-renamed", RefsOnly: true}

	_ = surfaceCounts(t, workspace, req, true)
	_, err := workspace.MoveSurfaces(req)

	require.ErrorIs(t, err, tool.ErrProjectAbsent)
}

// TestMove_ResumesAfterWitnessFlip guards finding A1's core invariant: a
// move whose session witnesses already point at the new path — because an
// earlier apply's sessions surface committed before a SIGKILL, but before
// the encoded project directory itself was promoted — still converges on
// a full re-run rather than hard-refusing on the flipped witness.
func TestMove_ResumesAfterWitnessFlip(t *testing.T) {
	home := testutil.SetupFixture(t)
	workspace := claude.NewWorkspace(home)
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"
	req := tool.MoveRequest{OldPath: oldPath, NewPath: newPath, RefsOnly: true}

	crashedSurfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	partialUndo := tool.NewRestorer()
	ranSessions := false
	for _, surface := range crashedSurfaces {
		if surface.Name != "sessions" {
			continue
		}
		result, err := surface.Apply(context.Background(), partialUndo)
		require.NoError(t, err)
		require.Positive(t, result.Count, "sanity: the crashed run's sessions surface must have rewritten a witness")
		ranSessions = true
	}
	require.True(t, ranSessions, "sanity: MoveSurfaces must include the sessions surface")
	partialUndo.Cleanup()

	resumedSurfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err, "a move whose session witnesses already point at the new path must still converge")
	undo := tool.NewRestorer()
	for _, surface := range resumedSurfaces {
		_, err := surface.Apply(context.Background(), undo)
		require.NoError(t, err, "apply %s", surface.Name)
	}
	undo.Cleanup()

	_, err = claude.LocateProject(home, oldPath)
	require.ErrorIs(t, err, tool.ErrProjectAbsent, "old path must no longer be locatable after convergence")
	locations, err := claude.LocateProject(home, newPath)
	require.NoError(t, err, "new path must be fully locatable and witness-consistent after convergence")
	assert.NotEmpty(t, locations.SessionTranscripts, "the project's transcripts must have carried over to the new path")
}

// TestMove_RefusesForeignWitness guards the foreign-collision non-negotiable:
// the fixture's encoded directory for "/Users/test/Projects/my-project"
// also holds a transcript witnessed by a session recording cwd
// "/Users/test/Projects/my project" (Claude's lossy encoder maps both to
// the same on-disk directory) — a THIRD path, neither this move's OldPath
// nor its NewPath. The resume path must never treat that as a match; it
// must hard refuse exactly as the single-path identity check already does.
func TestMove_RefusesForeignWitness(t *testing.T) {
	home := testutil.SetupFixture(t)
	workspace := claude.NewWorkspace(home)
	req := tool.MoveRequest{
		OldPath: "/Users/test/Projects/my-project",
		NewPath: "/Users/test/Projects/my-project-renamed",
	}

	_, err := workspace.MoveSurfaces(req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to rewrite")
	assert.Contains(t, err.Error(), `"/Users/test/Projects/my project"`,
		"error must name the witness cwd so the operator can identify the colliding project")
	assert.NotErrorIs(t, err, tool.ErrProjectAbsent, "a foreign collision must hard refuse, not degrade to absence")
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
