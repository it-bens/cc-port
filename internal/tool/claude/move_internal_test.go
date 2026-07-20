package claude

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

func TestConfigSurfacePlan_PropagatesUnreadableConfigFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; chmod cannot make the config unreadable")
	}
	configFile := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(configFile, []byte(`{"projects":{}}`), 0o600))
	require.NoError(t, os.Chmod(configFile, 0o000))
	t.Cleanup(func() { _ = os.Chmod(configFile, 0o600) })
	workspace := NewWorkspace(&Home{Dir: t.TempDir(), ConfigFile: configFile})

	_, err := workspace.configSurface(tool.MoveRequest{OldPath: "/old", NewPath: "/new"}).Plan(context.Background())

	require.Error(t, err)
	assert.ErrorContains(t, err, "read config file")
}

func TestHistorySurfaceApply_IsIdempotent(t *testing.T) {
	home := &Home{Dir: t.TempDir()}
	require.NoError(t, os.WriteFile(home.HistoryFile(), []byte(`{"project":"/old/project"}`+"\n"), 0o600))
	workspace := NewWorkspace(home)
	surface := workspace.historySurface(tool.MoveRequest{OldPath: "/old/project", NewPath: "/new/project"})

	first, err := surface.Apply(context.Background(), tool.NewRestorer())
	require.NoError(t, err)
	second, err := surface.Apply(context.Background(), tool.NewRestorer())
	require.NoError(t, err)

	assert.Equal(t, 1, first.Count)
	assert.Zero(t, second.Count)
}

// TestScanHistoryFile_CountsJSONEscapedPath pins Plan/Apply count parity for
// a path that appears in history.jsonl only in its JSON-escaped form
// ("\/" instead of "/"). Apply rewrites through StreamHistoryJSONL, which
// matches both forms; Plan must count the same occurrences or a dry-run
// preview undercounts a rename Apply actually performs.
func TestScanHistoryFile_CountsJSONEscapedPath(t *testing.T) {
	home := &Home{Dir: t.TempDir()}
	line := `{"project":"/other/project","display":"see \/old\/project\/main.go"}` + "\n"
	require.NoError(t, os.WriteFile(home.HistoryFile(), []byte(line), 0o600))
	workspace := NewWorkspace(home)
	surface := workspace.historySurface(tool.MoveRequest{OldPath: "/old/project", NewPath: "/new/project"})

	planned, err := surface.Plan(context.Background())
	require.NoError(t, err)
	applied, err := surface.Apply(context.Background(), tool.NewRestorer())
	require.NoError(t, err)

	assert.Equal(t, applied.Count, planned.Count,
		"a path referenced only in its JSON-escaped form must count the same in Plan and Apply")
	assert.Equal(t, 1, planned.Count)
}

func TestSnapshotPaths_EnumeratesProjectSnapshots(t *testing.T) {
	fileHistoryDir := filepath.Join(t.TempDir(), "file-history")
	firstSnapshot := filepath.Join(fileHistoryDir, "primary-session", "first@v1")
	secondSnapshot := filepath.Join(fileHistoryDir, "primary-session", "second@v2")
	thirdSnapshot := filepath.Join(fileHistoryDir, "secondary-session", "third@v1")
	for _, path := range []string{firstSnapshot, secondSnapshot, thirdSnapshot} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, nil, 0o600))
	}
	locations := &ProjectLocations{FileHistoryDirs: []string{
		filepath.Join(fileHistoryDir, "primary-session"),
		filepath.Join(fileHistoryDir, "secondary-session"),
	}}

	paths, err := snapshotPaths(t.Context(), locations)

	require.NoError(t, err)
	assert.Len(t, paths, 3)
	assert.Contains(t, paths, firstSnapshot)
	assert.Contains(t, paths, thirdSnapshot)
}

func TestSnapshotPaths_EmptyFileHistoryDirs(t *testing.T) {
	paths, err := snapshotPaths(t.Context(), &ProjectLocations{})

	require.NoError(t, err)
	assert.Empty(t, paths)
}

func TestRewriteTracked_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"cwd":"/old/proj"}`), 0o644)) //nolint:gosec // G306: test fixture in t.TempDir

	restorer := tool.NewRestorer()
	count, err := rewriteTracked(path, "/old/proj", "/new/proj", restorer)
	require.NoError(t, err)
	require.Equal(t, 1, count, "one occurrence must be replaced")

	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err)
	require.JSONEq(t, `{"cwd":"/new/proj"}`, string(got))

	// The registered snapshot must let a Restore reverse the rewrite,
	// proving rewriteTracked registered exactly the file it rewrote.
	require.NoError(t, restorer.Restore())
	restored, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err)
	require.JSONEq(t, `{"cwd":"/old/proj"}`, string(restored))
}

func TestRewriteTracked_SaveFails_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")

	_, err := rewriteTracked(path, "/old", "/new", tool.NewRestorer())
	require.Error(t, err, "expected error for missing file")
}

func TestRewriteTracked_NoReplacement_NoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	original := []byte(`{"unrelated":"content"}`)
	require.NoError(t, os.WriteFile(path, original, 0o644)) //nolint:gosec // G306: test fixture in t.TempDir

	count, err := rewriteTracked(path, "/old/proj", "/new/proj", tool.NewRestorer())
	require.NoError(t, err)
	require.Equal(t, 0, count, "no occurrence must be replaced")

	got, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err)
	require.True(t, bytes.Equal(got, original), "contents must not change")
}

func TestRewriteTracked_WriteFails_ReadOnlyDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; chmod 0500 will not prevent writes")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "file.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"cwd":"/old/proj"}`), 0o644)) //nolint:gosec // G306: test fixture in t.TempDir

	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // G302: deliberately read-only for the test
		t.Skipf("chmod unsupported: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o700) //nolint:gosec // G302: restore perms in test teardown
	})

	// Verify chmod is effective: attempt to create a file.
	probe := filepath.Join(dir, ".probe")
	if f, err := os.Create(probe); err == nil { //nolint:gosec // G304: path from t.TempDir
		_ = f.Close()
		_ = os.Remove(probe)
		t.Skip("chmod 0500 did not prevent writes on this filesystem")
	}

	_, err := rewriteTracked(path, "/old/proj", "/new/proj", tool.NewRestorer())
	require.Error(t, err, "expected error writing into read-only dir")
}

func TestApplyProjectDirectoryMove_ReportsResidualRemovalWithoutRollback(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old-project")
	newPath := filepath.Join(root, "new-project")
	home := &Home{Dir: filepath.Join(root, "dotclaude")}
	oldEncodedDir := home.ProjectDir(oldPath)
	require.NoError(t, os.MkdirAll(oldPath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(oldPath, "source.txt"), []byte("source"), 0o600))
	require.NoError(t, os.MkdirAll(oldEncodedDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(oldEncodedDir, "state.json"), []byte("state"), 0o600))

	originalRemoveAll := removeAll
	t.Cleanup(func() { removeAll = originalRemoveAll })
	removeAll = func(path string) error {
		if path == oldPath {
			return os.ErrPermission
		}
		return os.RemoveAll(path)
	}
	workspace := NewWorkspace(home)

	err := workspace.applyProjectDirectoryMove(t.Context(), tool.MoveRequest{OldPath: oldPath, NewPath: newPath}, tool.NewRestorer())

	require.NoError(t, err)
	assert.DirExists(t, newPath)
	assert.DirExists(t, home.ProjectDir(newPath))
	assert.DirExists(t, oldPath)
	assert.NoDirExists(t, oldEncodedDir)
	require.NotEmpty(t, workspace.moveWarningSnapshot())
	assert.Contains(t, workspace.moveWarningSnapshot()[0], "on-disk source directory still present")
}

// TestMoveSurfaces_RerunAfterPhysicalResidualFailureRefusesProjectAbsent
// covers the asymmetric case: a first Apply whose physical
// removeAll(OldPath) fails, while the encoded directory's own removal still
// succeeds, completes with a residual warning rather than failing outright.
// That leaves the on-disk pair split: the encoded directory at OldPath is
// gone, but the physical project directory at OldPath remains. A second
// Apply against the identical paths (with removeAll restored) can no longer
// resolve identity through OldPath's encoded directory and refuses with
// tool.ErrProjectAbsent, leaving the stranded physical directory in place
// rather than guessing at a rename target.
func TestMoveSurfaces_RerunAfterPhysicalResidualFailureRefusesProjectAbsent(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old-project")
	newPath := filepath.Join(root, "new-project")
	home := &Home{Dir: filepath.Join(root, "dotclaude")}
	oldEncodedDir := home.ProjectDir(oldPath)
	newEncodedDir := home.ProjectDir(newPath)
	require.NoError(t, os.MkdirAll(oldPath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(oldPath, "source.txt"), []byte("source"), 0o600))
	require.NoError(t, os.MkdirAll(oldEncodedDir, 0o750))
	const sessionUUID = "aaaaaaaa-0000-4000-8000-000000000004"
	require.NoError(t, os.WriteFile(filepath.Join(oldEncodedDir, sessionUUID+".jsonl"), []byte("{}"), 0o600))
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	sessionFile := fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, sessionUUID, oldPath)
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), "1.json"), []byte(sessionFile), 0o600))

	originalRemoveAll := removeAll
	t.Cleanup(func() { removeAll = originalRemoveAll })
	removeAll = func(path string) error {
		if path == oldPath {
			return os.ErrPermission
		}
		return os.RemoveAll(path)
	}
	workspace := NewWorkspace(home)
	req := tool.MoveRequest{OldPath: oldPath, NewPath: newPath}

	firstSurfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	firstUndo := tool.NewRestorer()
	for _, surface := range firstSurfaces {
		_, err := surface.Apply(t.Context(), firstUndo)
		require.NoError(t, err, "apply %s", surface.Name)
	}
	firstUndo.Cleanup()

	assert.NotEmpty(t, workspace.moveWarningSnapshot(), "a failed residual removal must surface as a warning")
	assert.DirExists(t, oldPath, "old physical directory must remain after its removal fails")
	assert.NoDirExists(t, oldEncodedDir, "old encoded directory's own removal must still succeed")
	assert.DirExists(t, newPath)
	assert.DirExists(t, newEncodedDir)

	removeAll = originalRemoveAll
	workspace.clearMoveWarnings()
	_, err = workspace.MoveSurfaces(req)
	require.ErrorIs(t, err, tool.ErrProjectAbsent)
	assert.DirExists(t, oldPath)
}

// witnesslessProjectWorkspace stages a project whose encoded directory holds
// data but which no sessions/*.json witness attributes to any path — the
// shape of every project whose sessions Claude has already rotated away, and
// the overwhelmingly common case for a cold project a user wants to move.
// Its transcript is named "session.jsonl", not a canonical UUID, so
// collectProjectDirEntries yields zero sessionUUIDs and
// verifyProjectMoveIdentity takes its `len(sessionUUIDs) == 0` skip branch.
func witnesslessProjectWorkspace(t *testing.T) (workspace *Workspace, home *Home, oldPath, newPath string) {
	t.Helper()
	return stageWitnesslessProject(t, "session.jsonl")
}

// uuidTranscriptWitnessedByNoSessionsWorkspace stages the same witness-less
// shape as witnesslessProjectWorkspace, except the transcript's filename IS a
// canonical session UUID, so collectProjectDirEntries yields one
// sessionUUID. With no sessions/ directory at all, walkSessionWitnesses still
// returns no cwds, so verifyProjectMoveIdentity instead takes its OTHER skip
// branch: `len(cwds) == 0`.
func uuidTranscriptWitnessedByNoSessionsWorkspace(t *testing.T) (workspace *Workspace, home *Home, oldPath, newPath string) {
	t.Helper()
	return stageWitnesslessProject(t, "11111111-2222-3333-4444-555555555555.jsonl")
}

func stageWitnesslessProject(t *testing.T, transcriptName string) (workspace *Workspace, home *Home, oldPath, newPath string) {
	t.Helper()
	root := t.TempDir()
	home = &Home{Dir: filepath.Join(root, "dotclaude")}
	oldPath = filepath.Join(root, "old-project")
	newPath = filepath.Join(root, "new-project")
	require.NoError(t, os.MkdirAll(home.ProjectDir(oldPath), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(home.ProjectDir(oldPath), transcriptName), []byte("{}\n"), 0o600))
	return NewWorkspace(home), home, oldPath, newPath
}

// countWarningOccurrences reports how many of warnings equal target: a
// dedup-sensitive assertion needs an exact count, since assert.Contains
// cannot distinguish one occurrence from an accidental duplicate.
func countWarningOccurrences(warnings []string, target string) int {
	count := 0
	for _, warning := range warnings {
		if warning == target {
			count++
		}
	}
	return count
}

func projectDirectorySurfaceOf(t *testing.T, surfaces []tool.Surface) tool.Surface {
	t.Helper()
	for _, surface := range surfaces {
		if surface.Name == tool.SurfaceProjectDirectory {
			return surface
		}
	}
	t.Fatal("MoveSurfaces must include the project-directory surface")
	return tool.Surface{}
}

func TestMoveSurfaces_PlansWitnesslessProjectDirectoryPromotion(t *testing.T) {
	workspace, home, oldPath, newPath := witnesslessProjectWorkspace(t)

	surfaces, err := workspace.MoveSurfaces(tool.MoveRequest{OldPath: oldPath, NewPath: newPath, RefsOnly: true})
	require.NoError(t, err)
	result, err := projectDirectorySurfaceOf(t, surfaces).Plan(t.Context())

	require.NoError(t, err, "a project with no session witness must still render a plan")
	assert.Equal(t, 1, result.Count)
	assert.Equal(t, 1, countWarningOccurrences(result.Warnings, identityCheckSkippedMessage(home.ProjectDir(oldPath), oldPath)),
		"the skipped identity check must surface as a structured plan warning exactly once")
}

// TestMoveSurfaces_PlansUUIDWitnessedProjectDirectoryPromotion covers
// verifyProjectMoveIdentity's OTHER skip branch: sessionUUIDs is non-empty
// (the encoded directory's only transcript carries a canonical UUID stem),
// but no sessions/ directory exists at all, so walkSessionWitnesses still
// returns no cwds.
func TestMoveSurfaces_PlansUUIDWitnessedProjectDirectoryPromotion(t *testing.T) {
	workspace, home, oldPath, newPath := uuidTranscriptWitnessedByNoSessionsWorkspace(t)

	surfaces, err := workspace.MoveSurfaces(tool.MoveRequest{OldPath: oldPath, NewPath: newPath, RefsOnly: true})
	require.NoError(t, err)
	result, err := projectDirectorySurfaceOf(t, surfaces).Plan(t.Context())

	require.NoError(t, err, "a project with a UUID-named transcript but no sessions/ directory must still render a plan")
	assert.Equal(t, 1, result.Count)
	assert.Equal(t, 1, countWarningOccurrences(result.Warnings, identityCheckSkippedMessage(home.ProjectDir(oldPath), oldPath)),
		"the skipped identity check must surface as a structured plan warning exactly once")
}

// TestResolveMoveIdentity_LocatesViaOldPathWhenWitnessesAlreadyFlipped pins
// the invariant that a move interrupted after sessionsSurface rewrote every
// witness to newPath, but before the encoded directory was promoted, must
// still resolve through oldPath. The data is readable only there, so
// reading the flipped witness as a foreign collision would name the sole
// surviving copy as the intruder and invite deleting it.
func TestResolveMoveIdentity_LocatesViaOldPathWhenWitnessesAlreadyFlipped(t *testing.T) {
	root := t.TempDir()
	home := &Home{Dir: filepath.Join(root, "dotclaude")}
	oldPath := filepath.Join(root, "old-project")
	newPath := filepath.Join(root, "new-project")
	sessionID := "11111111-2222-3333-4444-555555555555"
	require.NoError(t, os.MkdirAll(home.ProjectDir(oldPath), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(home.ProjectDir(oldPath), sessionID+".jsonl"), []byte("{}\n"), 0o600))
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(home.SessionsDir(), sessionID+".json"),
		fmt.Appendf(nil, `{"sessionId":%q,"cwd":%q}`, sessionID, newPath), 0o600))

	workspace := NewWorkspace(home)
	req := tool.MoveRequest{OldPath: oldPath, NewPath: newPath, RefsOnly: true}

	identity, err := workspace.resolveMoveIdentityState(req)
	require.NoError(t, err, "a witness already naming newPath must not read as a foreign collision")
	assert.Equal(t, oldPath, identity.locatePath,
		"the encoded directory is still at oldPath, so the move must locate it there")

	surfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	undo := tool.NewRestorer()
	for _, surface := range surfaces {
		_, err := surface.Apply(t.Context(), undo)
		require.NoError(t, err, "apply %s", surface.Name)
	}
	undo.Cleanup()

	assert.NoDirExists(t, home.ProjectDir(oldPath), "the interrupted move must converge, retiring the old encoded directory")
	assert.FileExists(t, filepath.Join(home.ProjectDir(newPath), sessionID+".jsonl"),
		"the project's data must land at the new encoded directory")
}

// TestStrandedStagingWarnings_ReportsDanglingSymlink pins the plan and apply
// on one probe: removeStagingDir refuses a non-directory staging entry with
// Lstat, so the plan must Lstat too, or a dangling staging symlink reads as
// absent in the plan and then blocks the apply.
func TestStrandedStagingWarnings_ReportsDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "new-project")
	staging := destination + rewrite.StagingSuffix
	require.NoError(t, os.Symlink(filepath.Join(root, "missing-target"), staging))

	warnings, err := strandedStagingWarnings([]string{destination})

	require.NoError(t, err)
	assert.Len(t, warnings, 1, "a dangling staging symlink must not read as absent")
	assert.Contains(t, warnings[0], staging)
}

func TestMoveSurfaces_AppliesWitnesslessProjectDirectoryPromotion(t *testing.T) {
	workspace, home, oldPath, newPath := witnesslessProjectWorkspace(t)

	surfaces, err := workspace.MoveSurfaces(tool.MoveRequest{OldPath: oldPath, NewPath: newPath, RefsOnly: true})
	require.NoError(t, err)
	undo := tool.NewRestorer()
	for _, surface := range surfaces {
		_, err := surface.Apply(t.Context(), undo)
		require.NoError(t, err, "apply %s", surface.Name)
	}
	undo.Cleanup()

	assert.NoDirExists(t, home.ProjectDir(oldPath), "the old encoded directory must be gone after convergence")
	assert.FileExists(t, filepath.Join(home.ProjectDir(newPath), "session.jsonl"),
		"the project's data must have carried over to the new encoded directory")
}

// TestMoveSurfaces_ApplyCarriesWitnesslessIdentityWarning pins the warning on
// the apply path, not only the plan: a --apply run never invokes Plan, so a
// skip warning routed through Plan alone would let a witnessless apply proceed
// silently.
func TestMoveSurfaces_ApplyCarriesWitnesslessIdentityWarning(t *testing.T) {
	workspace, home, oldPath, newPath := witnesslessProjectWorkspace(t)

	surfaces, err := workspace.MoveSurfaces(tool.MoveRequest{OldPath: oldPath, NewPath: newPath, RefsOnly: true})
	require.NoError(t, err)
	result, err := projectDirectorySurfaceOf(t, surfaces).Apply(t.Context(), tool.NewRestorer())

	require.NoError(t, err)
	assert.Equal(t, 1, countWarningOccurrences(result.Warnings, identityCheckSkippedMessage(home.ProjectDir(oldPath), oldPath)),
		"the skipped identity check must surface on the apply path exactly once")
}

func TestResolveMoveIdentity_RefusesThirdPathWitness(t *testing.T) {
	root := t.TempDir()
	home := &Home{Dir: filepath.Join(root, "dotclaude")}
	oldPath := "/Users/test/old-project"
	newPath := "/Users/test/new-project"
	thirdPath := "/Users/test/unrelated-project"
	const sessionUUID = "aaaaaaaa-0000-4000-8000-000000000003"
	require.NoError(t, os.MkdirAll(home.ProjectDir(oldPath), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home.ProjectDir(oldPath), sessionUUID+".jsonl"), []byte("{}"), 0o600))
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	sessionFile := fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, sessionUUID, thirdPath)
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), "1.json"), []byte(sessionFile), 0o600))

	_, err := NewWorkspace(home).resolveMoveIdentity(tool.MoveRequest{OldPath: oldPath, NewPath: newPath})

	require.Error(t, err)
	assert.Contains(t, err.Error(), thirdPath)
}

// TestRemoveStagingDir_ReconcilesEmptyStaging proves removeStagingDir
// deletes an empty staging directory: PromoteDir creates the staging
// directory before it copies anything into it, so a crash in that narrow
// window strands an empty directory that holds no data and is always safe
// to reconcile.
func TestRemoveStagingDir_ReconcilesEmptyStaging(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "new-project")
	staging := destination + rewrite.StagingSuffix
	require.NoError(t, os.MkdirAll(staging, 0o750))

	err := removeStagingDir(destination, filepath.Join(root, "old-project"))

	require.NoError(t, err)
	assert.NoDirExists(t, staging)
}

func TestRemoveStagingDir_RefusesLocatableProject(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "new-project")
	source := filepath.Join(root, "old-project")
	staging := destination + rewrite.StagingSuffix
	require.NoError(t, os.MkdirAll(staging, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(staging, "partial"), []byte("partial"), 0o600))

	err := removeStagingDir(destination, source)

	require.Error(t, err)
	require.ErrorContains(t, err, staging)
	assert.DirExists(t, staging)
	assert.FileExists(t, filepath.Join(staging, "partial"))
	stagingSource := filepath.Join(root, "source"+rewrite.StagingSuffix)
	require.NoError(t, os.MkdirAll(stagingSource, 0o750))
	require.Error(t, removeStagingDir(filepath.Join(root, "source"), stagingSource))
	assert.DirExists(t, stagingSource, "a staging==source refusal must never delete the directory it refused to touch")
}

// TestRemoveStagingDir_AbsentStagingIsNoOp proves removeStagingDir returns
// nil, not an error, when nothing is stranded at the staging path.
func TestRemoveStagingDir_AbsentStagingIsNoOp(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "new-project")

	err := removeStagingDir(destination, filepath.Join(root, "old-project"))

	require.NoError(t, err)
}

// TestRemoveStagingDir_RefusesSymlinkStaging proves a staging path that is a
// symlink to an empty directory elsewhere is refused rather than unlinked:
// os.Lstat classifies the symlink itself, so IsDir() is false and the
// existing "not a directory" refusal fires before os.RemoveAll ever runs
// against a path cc-port never created.
func TestRemoveStagingDir_RefusesSymlinkStaging(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "new-project")
	staging := destination + rewrite.StagingSuffix
	target := filepath.Join(root, "foreign-empty-dir")
	require.NoError(t, os.MkdirAll(target, 0o750))
	require.NoError(t, os.Symlink(target, staging))

	err := removeStagingDir(destination, filepath.Join(root, "old-project"))

	require.Error(t, err)
	require.ErrorContains(t, err, staging)
	assert.DirExists(t, target, "the symlink target must survive a refused staging path")
	info, lstatErr := os.Lstat(staging)
	require.NoError(t, lstatErr, "the symlink itself must survive a refused staging path")
	assert.Equal(t, os.ModeSymlink, info.Mode()&os.ModeSymlink)
}

// TestClassifyDestination_RefusesDanglingSymlinkDestinationWhenSourceRemains
// proves a destination that is a dangling symlink (its target missing) is
// classified as EXISTING via os.Lstat, not absent via os.Stat: os.Stat
// follows the symlink and would report fs.ErrNotExist, misclassifying the
// destination as absent and letting promotion proceed toward an obscure
// os.Rename failure instead of refusing cleanly and naming both paths.
func TestClassifyDestination_RefusesDanglingSymlinkDestinationWhenSourceRemains(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "old-project")
	destination := filepath.Join(root, "new-project")
	danglingTarget := filepath.Join(root, "missing-target")
	require.NoError(t, os.MkdirAll(source, 0o750))
	require.NoError(t, os.Symlink(danglingTarget, destination))

	state, err := classifyDestination(source, destination)

	require.NoError(t, err)
	assert.Equal(t, destinationRefused, state)
	_, lstatErr := os.Lstat(destination)
	require.NoError(t, lstatErr, "the dangling symlink must survive classification")
}
