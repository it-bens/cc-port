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

// TestApplyProjectDirectoryMove_RerunAfterPhysicalResidualFailureConverges proves
// the crash-then-resume contract end to end for the asymmetric case: a first
// Apply whose physical removeAll(OldPath) fails, while the encoded
// directory's own removal still succeeds, completes with a residual warning
// rather than failing outright. That leaves the encoded pair in "old source
// already gone, new destination already exists" — the exact shape a resumed
// Apply must still converge on rather than mistake for a foreign collision.
// A second Apply against the identical paths (with removeAll restored)
// converges, removing the leftover physical directory and completing
// warning-free.
func TestApplyProjectDirectoryMove_RerunAfterPhysicalResidualFailureConverges(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old-project")
	newPath := filepath.Join(root, "new-project")
	home := &Home{Dir: filepath.Join(root, "dotclaude")}
	oldEncodedDir := home.ProjectDir(oldPath)
	newEncodedDir := home.ProjectDir(newPath)
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
	req := tool.MoveRequest{OldPath: oldPath, NewPath: newPath}

	firstErr := workspace.applyProjectDirectoryMove(t.Context(), req, tool.NewRestorer())

	require.NoError(t, firstErr, "warn-and-complete must not fail the apply")
	assert.NotEmpty(t, workspace.moveWarningSnapshot(), "a failed residual removal must surface as a warning")
	assert.DirExists(t, oldPath, "old physical directory must remain after its removal fails")
	assert.NoDirExists(t, oldEncodedDir, "old encoded directory's own removal must still succeed")
	assert.DirExists(t, newPath)
	assert.DirExists(t, newEncodedDir)

	removeAll = originalRemoveAll
	workspace.clearMoveWarnings()
	secondErr := workspace.applyProjectDirectoryMove(t.Context(), req, tool.NewRestorer())

	require.NoError(t, secondErr, "the resumed apply must complete cleanly")
	assert.NoDirExists(t, oldPath, "re-run must finish removing the leftover physical directory")
	assert.Empty(t, workspace.moveWarningSnapshot(), "a clean re-run must not carry forward a residual warning")
}

// TestResolveMoveIdentity_ResumesViaNewPathAfterEncodedDirPromoted guards
// the OTHER resume sub-case of finding A1 (the "sessions surface still
// hasn't run yet, but the encoded directory has ALREADY been promoted"
// window is covered by TestMove_ResumesAfterWitnessFlip in
// move_contract_test.go): a SIGKILL after projectDirectorySurface has
// promoted OldPath's encoded directory to NewPath's (writing a marker
// recording OldPath as its source) but before its final
// removeAll(oldProjectDir) runs leaves OldPath's encoded directory gone
// entirely. resolveMoveIdentity must fall back to NewPath and accept it
// ONLY because the marker proves it was promoted from exactly OldPath —
// not merely because its witnesses happen to say NewPath, which an
// unrelated, coincidentally pre-existing project would too.
func TestResolveMoveIdentity_ResumesViaNewPathAfterEncodedDirPromoted(t *testing.T) {
	root := t.TempDir()
	home := &Home{Dir: filepath.Join(root, "dotclaude")}
	oldPath := "/Users/test/old-project"
	newPath := "/Users/test/new-project"
	oldEncodedDir := home.ProjectDir(oldPath)
	newEncodedDir := home.ProjectDir(newPath)

	const sessionUUID = "aaaaaaaa-0000-4000-8000-000000000001"
	require.NoError(t, os.MkdirAll(newEncodedDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(newEncodedDir, sessionUUID+".jsonl"), []byte("{}"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(newEncodedDir, rewrite.MarkerFilename), []byte(oldEncodedDir), 0o600))
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	sessionFile := fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, sessionUUID, newPath)
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), "1.json"), []byte(sessionFile), 0o600))
	workspace := NewWorkspace(home)
	req := tool.MoveRequest{OldPath: oldPath, NewPath: newPath}

	locatePath, err := workspace.resolveMoveIdentity(req)

	require.NoError(t, err)
	assert.Equal(t, newPath, locatePath)
}

// TestResolveMoveIdentity_RefusesNewPathWithoutMarker proves the negative
// space of the same fallback: a directory at NewPath's encoded location
// whose OWN witnesses genuinely say NewPath (so verifyProjectMoveIdentity
// alone would accept it) but that carries no promotion marker recording
// OldPath must NOT be treated as this move's resume target — it degrades
// to tool.ErrProjectAbsent instead, matching what a wholly unrelated
// NewPath with no evidence at all would also produce, so no surface ever
// touches a coincidentally pre-existing, unrelated project's data.
func TestResolveMoveIdentity_RefusesNewPathWithoutMarker(t *testing.T) {
	root := t.TempDir()
	home := &Home{Dir: filepath.Join(root, "dotclaude")}
	oldPath := "/Users/test/old-project"
	newPath := "/Users/test/new-project"
	newEncodedDir := home.ProjectDir(newPath)

	const sessionUUID = "aaaaaaaa-0000-4000-8000-000000000002"
	require.NoError(t, os.MkdirAll(newEncodedDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(newEncodedDir, sessionUUID+".jsonl"), []byte("{}"), 0o600))
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	sessionFile := fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, sessionUUID, newPath)
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), "1.json"), []byte(sessionFile), 0o600))
	workspace := NewWorkspace(home)
	req := tool.MoveRequest{OldPath: oldPath, NewPath: newPath}

	_, err := workspace.resolveMoveIdentity(req)

	require.ErrorIs(t, err, tool.ErrProjectAbsent)
}

// TestResolveMoveIdentity_ResumesWitnessLessProjectWithValidMarker guards
// finding A1's witness-less sub-case: a project with no session UUIDs at
// all (so verifyProjectMoveIdentity resolves identityFresh, the deliberate
// skip-with-warning case, never identityResume) whose encoded directory is
// already published at NewPath with a marker naming OldPath must still
// resume. The marker, not the witness set, is the identity oracle on this
// fallback branch; witnesses serve only as the foreign-collision veto
// verifyProjectMoveIdentity already ran without error.
func TestResolveMoveIdentity_ResumesWitnessLessProjectWithValidMarker(t *testing.T) {
	root := t.TempDir()
	home := &Home{Dir: filepath.Join(root, "dotclaude")}
	oldPath := "/Users/test/old-project"
	newPath := "/Users/test/new-project"
	oldEncodedDir := home.ProjectDir(oldPath)
	newEncodedDir := home.ProjectDir(newPath)

	require.NoError(t, os.MkdirAll(newEncodedDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(newEncodedDir, rewrite.MarkerFilename), []byte(oldEncodedDir), 0o600))
	workspace := NewWorkspace(home)
	req := tool.MoveRequest{OldPath: oldPath, NewPath: newPath}

	locatePath, err := workspace.resolveMoveIdentity(req)

	require.NoError(t, err)
	assert.Equal(t, newPath, locatePath)
}

// TestResolveMoveIdentity_RefusesForeignPromotionMarker proves the negative
// space of the marker-mismatch case: NewPath's encoded directory carries a
// marker that positively names a THIRD, unrelated source, not OldPath. That
// is demonstrated evidence of a foreign promotion, so resolveMoveIdentity
// must hard refuse and name the recorded source rather than degrade to
// tool.ErrProjectAbsent — the same misclassification a bool-only marker
// check cannot avoid, because it collapses "absent" and "names someone
// else" into the same false. The witness here genuinely says NewPath (so
// verifyProjectMoveIdentity alone would accept it), isolating the
// assertion to the marker check.
func TestResolveMoveIdentity_RefusesForeignPromotionMarker(t *testing.T) {
	root := t.TempDir()
	home := &Home{Dir: filepath.Join(root, "dotclaude")}
	oldPath := "/Users/test/old-project"
	newPath := "/Users/test/new-project"
	newEncodedDir := home.ProjectDir(newPath)
	foreignSource := "/Users/test/unrelated-project"

	const sessionUUID = "aaaaaaaa-0000-4000-8000-000000000003"
	require.NoError(t, os.MkdirAll(newEncodedDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(newEncodedDir, sessionUUID+".jsonl"), []byte("{}"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(newEncodedDir, rewrite.MarkerFilename), []byte(foreignSource), 0o600))
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	sessionFile := fmt.Sprintf(`{"sessionId":%q,"cwd":%q}`, sessionUUID, newPath)
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), "1.json"), []byte(sessionFile), 0o600))
	workspace := NewWorkspace(home)
	req := tool.MoveRequest{OldPath: oldPath, NewPath: newPath}

	_, err := workspace.resolveMoveIdentity(req)

	require.Error(t, err)
	require.NotErrorIs(t, err, tool.ErrProjectAbsent, "a marker naming a third, unrelated source is a foreign promotion, not an absent project")
	assert.Contains(t, err.Error(), foreignSource, "the error must name the recorded source")
}

// TestRemoveStagingDir_ReconcilesEmptyStaging proves removeStagingDir
// deletes an empty staging directory even without a marker: PromoteDir
// creates the staging directory before it writes the marker, so a crash in
// that narrow window strands an empty directory that holds no data and is
// always safe to reconcile.
func TestRemoveStagingDir_ReconcilesEmptyStaging(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "new-project")
	staging := destination + rewrite.StagingSuffix
	require.NoError(t, os.MkdirAll(staging, 0o750))

	err := removeStagingDir(destination, filepath.Join(root, "old-project"))

	require.NoError(t, err)
	assert.NoDirExists(t, staging)
}

// TestRemoveStagingDir_RefusesLocatableProject proves removeStagingDir
// refuses rather than deletes whenever the staging path's marker cannot be
// verified against the passed source: a real, marker-less directory sitting
// at the staging path, and a staging directory whose marker was written for
// a different promotion, are both indistinguishable from foreign data
// without a marker recording exactly this source, so both must refuse.
func TestRemoveStagingDir_RefusesLocatableProject(t *testing.T) {
	tests := []struct {
		name        string
		writeMarker bool
		markerValue string
	}{
		{name: "no marker at all"},
		{name: "marker records a different source", writeMarker: true, markerValue: "/some/other/project"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			destination := filepath.Join(root, "new-project")
			source := filepath.Join(root, "old-project")
			staging := destination + rewrite.StagingSuffix
			require.NoError(t, os.MkdirAll(staging, 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(staging, "session.json"), []byte(`{"cwd":"/some/real/project"}`), 0o600))
			if test.writeMarker {
				require.NoError(t, os.WriteFile(filepath.Join(staging, rewrite.MarkerFilename), []byte(test.markerValue), 0o600))
			}

			err := removeStagingDir(destination, source)

			require.Error(t, err)
			assert.DirExists(t, staging, "a refused staging directory must remain on disk")
			assert.FileExists(t, filepath.Join(staging, "session.json"))
		})
	}
}

// TestRemoveStagingDir_ReconcilesOwnStrandedStaging proves removeStagingDir
// deletes a staging directory whose marker content matches the passed
// source: a stranded copy from this exact promotion is safe to discard so a
// retry restarts from a clean copy — the crash-safe convergence path.
func TestRemoveStagingDir_ReconcilesOwnStrandedStaging(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "new-project")
	source := filepath.Join(root, "old-project")
	staging := destination + rewrite.StagingSuffix
	require.NoError(t, os.MkdirAll(staging, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(staging, "partial"), []byte("partial"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(staging, rewrite.MarkerFilename), []byte(source), 0o600))

	err := removeStagingDir(destination, source)

	require.NoError(t, err)
	assert.NoDirExists(t, staging)
}

// TestRemoveStagingDir_AbsentStagingIsNoOp proves removeStagingDir returns
// nil, not an error, when nothing is stranded at the staging path.
func TestRemoveStagingDir_AbsentStagingIsNoOp(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "new-project")

	err := removeStagingDir(destination, filepath.Join(root, "old-project"))

	require.NoError(t, err)
}
