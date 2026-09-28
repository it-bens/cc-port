package claude_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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
				preflightSurfaces, err := workspace.MoveSurfaces(t.Context(), req)
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
	_, err := workspace.MoveSurfaces(t.Context(), req)

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

	crashedSurfaces, err := workspace.MoveSurfaces(t.Context(), req)
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

	resumedSurfaces, err := workspace.MoveSurfaces(t.Context(), req)
	require.NoError(t, err, "a move whose session witnesses already point at the new path must still converge")
	undo := tool.NewRestorer()
	for _, surface := range resumedSurfaces {
		_, err := surface.Apply(context.Background(), undo)
		require.NoError(t, err, "apply %s", surface.Name)
	}
	undo.Cleanup()

	_, err = claude.LocateProject(t.Context(), home, oldPath)
	require.ErrorIs(t, err, tool.ErrProjectAbsent, "old path must no longer be locatable after convergence")
	locations, err := claude.LocateProject(t.Context(), home, newPath)
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

	_, err := workspace.MoveSurfaces(t.Context(), req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to rewrite")
	assert.Contains(t, err.Error(), `"/Users/test/Projects/my project"`,
		"error must name the witness cwd so the operator can identify the colliding project")
	assert.NotErrorIs(t, err, tool.ErrProjectAbsent, "a foreign collision must hard refuse, not degrade to absence")
}

// TestMove_LeavesUnreferencedHomeWideFilesUntouched pins the no-op rule on
// the home-wide surfaces: history.jsonl, ~/.claude.json, and settings.json
// are shared across every project, so a move of a project none of them names
// must leave each file's inode and mtime exactly as they were. A writer that
// replaces the file anyway keeps its bytes but not its inode.
func TestMove_LeavesUnreferencedHomeWideFilesUntouched(t *testing.T) {
	root := t.TempDir()
	home := &claude.Home{
		Dir:        filepath.Join(root, "dotclaude"),
		ConfigFile: filepath.Join(root, "dotclaude.json"),
	}
	oldPath := filepath.Join(root, "old-project")
	newPath := filepath.Join(root, "new-project")
	require.NoError(t, os.MkdirAll(home.ProjectDir(oldPath), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(home.ProjectDir(oldPath), "primary-session.jsonl"), []byte("{}\n"), 0o600))

	unreferenced := map[string]string{
		home.HistoryFile():  `{"display":"unrelated","project":"/Users/test/Projects/otherproject"}` + "\n",
		home.ConfigFile:     `{"projects":{"/Users/test/Projects/otherproject":{"allowedTools":[]}}}`,
		home.SettingsFile(): `{"env":{"PROJECT_ROOT":"/Users/test/Projects/otherproject"}}`,
	}
	past := time.Date(2020, time.March, 1, 12, 0, 0, 0, time.UTC)
	before := make(map[string]os.FileInfo, len(unreferenced))
	for path, contents := range unreferenced {
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
		require.NoError(t, os.Chtimes(path, past, past))
		info, err := os.Stat(path)
		require.NoError(t, err)
		before[path] = info
	}

	workspace := claude.NewWorkspace(home)
	surfaces, err := workspace.MoveSurfaces(t.Context(), tool.MoveRequest{OldPath: oldPath, NewPath: newPath, RefsOnly: true})
	require.NoError(t, err)
	undo := tool.NewRestorer()
	for _, surface := range surfaces {
		_, err := surface.Apply(context.Background(), undo)
		require.NoError(t, err, "apply %s", surface.Name)
	}
	undo.Cleanup()

	for path, want := range before {
		got, err := os.Stat(path)
		require.NoError(t, err)
		assert.True(t, os.SameFile(want, got), "%s must keep its inode", path)
		assert.Equal(t, want.ModTime(), got.ModTime(), "%s must keep its mtime", path)
	}
}

// TestMove_LeavesFilesWithoutOldPathReferenceUntouched pins the no-op rule on
// the per-project writers: a session-keyed registry file the moved project's
// session UUID makes the writer enumerate, and a memory file naming neither
// the project path nor its encoded directory form, each keep their bytes,
// inode, and mtime. The project-directory surface is left out of the apply,
// because its directory copy gives every file below the encoded directory a
// fresh inode whatever the writers did.
func TestMove_LeavesFilesWithoutOldPathReferenceUntouched(t *testing.T) {
	home := testutil.SetupFixture(t)
	workspace := claude.NewWorkspace(home)
	oldPath := testutil.FixtureProjectPath()
	newPath := oldPath + "-renamed"
	// The fixture project directory holds this session's transcript, so the
	// session-keyed registries enumerate every per-session file under this UUID.
	const facetSessionUUID = "a1b2c3d4-0000-0000-0000-000000000001"

	untouched := []struct {
		path      string
		forbidden []string
	}{
		{
			path:      filepath.Join(home.UsageDataDir(), "facets", facetSessionUUID+".json"),
			forbidden: []string{oldPath},
		},
		{
			path:      filepath.Join(home.ProjectDir(oldPath), "memory", "owner_contacts.md"),
			forbidden: []string{oldPath, home.ProjectDir(oldPath)},
		},
	}

	type snapshot struct {
		info os.FileInfo
		data []byte
	}
	before := make([]snapshot, len(untouched))
	for index, file := range untouched {
		path := file.path
		data, err := os.ReadFile(path) //nolint:gosec // G304: path inside the staged fixture
		require.NoError(t, err)
		for _, forbidden := range file.forbidden {
			require.NotContains(t, string(data), forbidden,
				"fixture sanity: %s must not carry a reference the move would rewrite", path)
		}
		info, err := os.Stat(path)
		require.NoError(t, err)
		before[index] = snapshot{info: info, data: data}
	}

	surfaces, err := workspace.MoveSurfaces(t.Context(), tool.MoveRequest{OldPath: oldPath, NewPath: newPath, RefsOnly: true})
	require.NoError(t, err)
	undo := tool.NewRestorer()
	for _, surface := range surfaces {
		if surface.Name == tool.SurfaceProjectDirectory {
			continue
		}
		_, err := surface.Apply(context.Background(), undo)
		require.NoError(t, err, "apply %s", surface.Name)
	}
	undo.Cleanup()

	for index, file := range untouched {
		path := file.path
		data, err := os.ReadFile(path) //nolint:gosec // G304: path inside the staged fixture
		require.NoError(t, err)
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, before[index].data, data, "%s must keep its bytes", path)
		assert.True(t, os.SameFile(before[index].info, info), "%s must keep its inode", path)
		assert.Equal(t, before[index].info.ModTime(), info.ModTime(), "%s must keep its mtime", path)
	}
}

func surfaceCounts(t *testing.T, workspace *claude.Workspace, req tool.MoveRequest, apply bool) map[string]int {
	t.Helper()
	surfaces, err := workspace.MoveSurfaces(t.Context(), req)
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
