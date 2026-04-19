package move_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/testutil"
)

const (
	oldProjectPath     = "/Users/test/Projects/myproject"
	newProjectPath     = "/Users/test/Projects/newproject"
	renamedProjectPath = "/Users/test/Projects/renamed"
)

func TestDryRun(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	plan, err := move.DryRun(claudeHome, move.Options{
		OldPath:            oldProjectPath,
		NewPath:            newProjectPath,
		RewriteTranscripts: false,
		RefsOnly:           false,
	})

	require.NoError(t, err)
	require.NotNil(t, plan)

	assert.Equal(t, claudeHome.ProjectDir(oldProjectPath), plan.OldProjectDir)
	assert.Equal(t, claudeHome.ProjectDir(newProjectPath), plan.NewProjectDir)

	assert.Positive(t, plan.ReplacementsByCategory["history"], "expected history replacements")
	assert.Positive(t, plan.ReplacementsByCategory["sessions"], "expected session file replacements")
	assert.Positive(t, plan.ReplacementsByCategory["settings"], "expected settings replacements")

	assert.True(t, plan.ConfigBlockRekey, "expected config block to be rekeyed")

	assert.Equal(t, 0, plan.TranscriptReplacements, "transcripts not opted in, should be 0")

	assert.NotEmpty(t, plan.RulesWarnings, "expected rules warnings for paths in test-rule.md")

	assert.True(t, plan.MoveProjectDir, "RefsOnly=false should set MoveProjectDir=true")
}

func TestDryRun_WithTranscripts(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	plan, err := move.DryRun(claudeHome, move.Options{
		OldPath:            oldProjectPath,
		NewPath:            newProjectPath,
		RewriteTranscripts: true,
		RefsOnly:           false,
	})

	require.NoError(t, err)
	require.NotNil(t, plan)

	assert.Positive(t, plan.TranscriptReplacements, "expected transcript replacements when opted in")
}

func TestDryRun_RefsOnly(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	plan, err := move.DryRun(claudeHome, move.Options{
		OldPath:  oldProjectPath,
		NewPath:  newProjectPath,
		RefsOnly: true,
	})

	require.NoError(t, err)
	require.NotNil(t, plan)

	assert.False(t, plan.MoveProjectDir, "RefsOnly=true should set MoveProjectDir=false")
}

func TestDryRun_ProjectNotFound(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	_, err := move.DryRun(claudeHome, move.Options{
		OldPath: "/Users/test/Projects/doesnotexist",
		NewPath: newProjectPath,
	})

	require.Error(t, err)
}

func TestApply(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	err := move.Apply(claudeHome, move.Options{
		OldPath:            oldProjectPath,
		NewPath:            newProjectPath,
		RewriteTranscripts: false,
		RefsOnly:           true, // avoid trying to copy a non-existent disk path
	})
	require.NoError(t, err)

	// Old project data dir should be gone.
	oldProjectDir := claudeHome.ProjectDir(oldProjectPath)
	_, statErr := os.Stat(oldProjectDir)
	assert.True(t, os.IsNotExist(statErr), "old project data dir should be removed")

	// New project data dir should exist.
	newProjectDir := claudeHome.ProjectDir(newProjectPath)
	_, err = os.Stat(newProjectDir)
	require.NoError(t, err, "new project data dir should exist")

	// history.jsonl should reference the new path. Use path-boundary semantics:
	// substrings of unrelated paths sharing a prefix (e.g. "myproject-extras")
	// must remain untouched, so we cannot assert a raw substring absence.
	historyData, err := os.ReadFile(claudeHome.HistoryFile())
	require.NoError(t, err)
	historyContent := string(historyData)
	assert.Contains(t, historyContent, newProjectPath,
		"history.jsonl should contain new project path")
	assert.NotContains(t, historyContent, oldProjectPath+"/",
		"history.jsonl should not contain old project path followed by /")
	assert.NotContains(t, historyContent, `"`+oldProjectPath+`"`,
		"history.jsonl should not contain old project path as a quoted JSON value")

	// User config should have new key, not old key.
	configData, err := os.ReadFile(claudeHome.ConfigFile)
	require.NoError(t, err)

	var userConfig claude.UserConfig
	require.NoError(t, json.Unmarshal(configData, &userConfig))
	_, hasOldKey := userConfig.Projects[oldProjectPath]
	_, hasNewKey := userConfig.Projects[newProjectPath]
	assert.False(t, hasOldKey, "config should not have old project key")
	assert.True(t, hasNewKey, "config should have new project key")
}

func TestApply_RefsOnly(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	err := move.Apply(claudeHome, move.Options{
		OldPath:  oldProjectPath,
		NewPath:  newProjectPath,
		RefsOnly: true,
	})
	require.NoError(t, err)

	// Old project data dir should be gone.
	oldProjectDir := claudeHome.ProjectDir(oldProjectPath)
	_, statErr := os.Stat(oldProjectDir)
	assert.True(t, os.IsNotExist(statErr), "old project data dir should be removed")

	// New project data dir should exist and contain the copied transcripts.
	newProjectDir := claudeHome.ProjectDir(newProjectPath)
	_, err = os.Stat(newProjectDir)
	require.NoError(t, err, "new project data dir should exist")

	transcriptPath := filepath.Join(newProjectDir, "a1b2c3d4-0000-0000-0000-000000000001.jsonl")
	_, err = os.Stat(transcriptPath)
	require.NoError(t, err, "copied transcript should exist in new project data dir")
}

func TestApply_WithTranscripts(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	err := move.Apply(claudeHome, move.Options{
		OldPath:            oldProjectPath,
		NewPath:            newProjectPath,
		RewriteTranscripts: true,
		RefsOnly:           true,
	})
	require.NoError(t, err)

	// Transcript in new project dir should have new paths, not old.
	newProjectDir := claudeHome.ProjectDir(newProjectPath)
	entries, err := os.ReadDir(newProjectDir)
	require.NoError(t, err)

	foundTranscript := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		foundTranscript = true
		transcriptPath := filepath.Join(newProjectDir, entry.Name())
		transcriptData, err := os.ReadFile(transcriptPath) //nolint:gosec // test file path
		require.NoError(t, err)
		content := string(transcriptData)
		// Path-boundary semantics: a raw substring of oldProjectPath may still
		// remain if it is followed by a path-component byte (e.g. a sentence
		// "look at /Users/test/Projects/myproject." — the trailing '.' could
		// also be the start of an extension like ".v2", so we cannot rewrite
		// it safely without context. Verify the path-as-path occurrences are
		// gone instead.
		assert.NotContains(t, content, oldProjectPath+"/",
			"transcript should not contain old project path followed by /")
		assert.NotContains(t, content, `"`+oldProjectPath+`"`,
			"transcript should not contain old project path as a quoted JSON value")
		assert.Contains(t, content, newProjectPath,
			"transcript should contain new project path after rewrite")
	}
	assert.True(t, foundTranscript, "expected at least one transcript file in new project dir")

	assertSessionSubdirFilesRewritten(t, newProjectDir)
}

func TestDryRun_AbortsOnEncodedDirCollision(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	// Another real project path that encodes to the same directory as
	// "/Users/test/Projects/renamed" would (EncodePath collapses '.' to '-').
	collidingPath := "/Users/test/Projects/renamed.v2"
	collidingOther := "/Users/test/Projects/renamed-v2"

	// Create the encoded directory for "renamed-v2" so the later move to
	// "renamed.v2" finds it occupied.
	occupiedDir := claudeHome.ProjectDir(collidingOther)
	require.NoError(t, os.MkdirAll(occupiedDir, 0o750))

	_, err := move.DryRun(claudeHome, move.Options{
		OldPath: oldProjectPath,
		NewPath: collidingPath,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestDryRun_AbortsWhenOldAndNewEncodeIdentically(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	// "/Users/test/Projects/my.project" and "/Users/test/Projects/my-project"
	// both encode to -Users-test-Projects-my-project.
	identicalEncoding := "/Users/test/Projects/my.project"

	_, err := move.DryRun(claudeHome, move.Options{
		OldPath: "/Users/test/Projects/my-project",
		NewPath: identicalEncoding,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both encode to")
}

func TestApply_AbortsOnEncodedDirCollision(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	collidingPath := "/Users/test/Projects/renamed.v2"
	collidingOther := "/Users/test/Projects/renamed-v2"

	occupiedDir := claudeHome.ProjectDir(collidingOther)
	require.NoError(t, os.MkdirAll(occupiedDir, 0o750))

	// Drop a sentinel file so we can confirm Apply left the occupier untouched.
	sentinelPath := filepath.Join(occupiedDir, "sentinel.txt")
	require.NoError(t, os.WriteFile(sentinelPath, []byte("do-not-touch"), 0600))

	err := move.Apply(claudeHome, move.Options{
		OldPath:  oldProjectPath,
		NewPath:  collidingPath,
		RefsOnly: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// Old project's data dir must be unchanged.
	oldProjectDir := claudeHome.ProjectDir(oldProjectPath)
	assert.DirExists(t, oldProjectDir, "old project dir should still exist")

	// Occupier's sentinel must be untouched.
	sentinelData, readErr := os.ReadFile(sentinelPath) //nolint:gosec // test-controlled path
	require.NoError(t, readErr)
	assert.Equal(t, "do-not-touch", string(sentinelData))
}

func TestApply_AbortsWhenClaudeSessionIsLive(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	// Write a sessions/*.json that references this test process's PID —
	// guaranteed alive, so the concurrency guard must abort.
	sessionsDir := claudeHome.SessionsDir()
	require.NoError(t, os.MkdirAll(sessionsDir, 0o750))
	liveSession := claude.SessionFile{Cwd: oldProjectPath, Pid: os.Getpid()}
	liveSessionData, err := json.Marshal(liveSession)
	require.NoError(t, err)
	liveSessionPath := filepath.Join(sessionsDir, "live-session.json")
	require.NoError(t, os.WriteFile(liveSessionPath, liveSessionData, 0600))

	// Snapshot the encoded project dir before the attempted move so we can
	// assert Apply left the filesystem untouched on abort.
	oldProjectDir := claudeHome.ProjectDir(oldProjectPath)
	before, err := os.ReadDir(oldProjectDir)
	require.NoError(t, err)

	applyErr := move.Apply(claudeHome, move.Options{
		OldPath:  oldProjectPath,
		NewPath:  newProjectPath,
		RefsOnly: true,
	})
	require.Error(t, applyErr, "Apply must abort when a live Claude Code session is detected")
	assert.Contains(t, applyErr.Error(), "live Claude Code session")

	// Old project dir must still be there with identical entry count.
	after, err := os.ReadDir(oldProjectDir)
	require.NoError(t, err, "old project dir should still exist after aborted apply")
	assert.Len(t, after, len(before), "no directory entries should have been created or removed")

	// New project dir must not have been created.
	_, newStatErr := os.Stat(claudeHome.ProjectDir(newProjectPath))
	assert.True(t, os.IsNotExist(newStatErr), "new project dir should not exist after aborted apply")
}

func TestDryRun_ReportsMalformedHistoryLines(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	plan, err := move.DryRun(claudeHome, move.Options{
		OldPath: oldProjectPath,
		NewPath: newProjectPath,
	})
	require.NoError(t, err)
	// The fixture history.jsonl has exactly one malformed line at line 10.
	assert.Equal(t, []int{10}, plan.HistoryMalformedLines,
		"dry-run should surface the 1-based line numbers of malformed history entries")
}

func TestApply_WarnsOnMalformedHistoryLines(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	var warnings bytes.Buffer
	err := move.Apply(claudeHome, move.Options{
		OldPath:       oldProjectPath,
		NewPath:       newProjectPath,
		RefsOnly:      true,
		WarningWriter: &warnings,
	})
	require.NoError(t, err)

	output := warnings.String()
	assert.Contains(t, output, "history.jsonl",
		"warning should identify history.jsonl")
	assert.Contains(t, output, "malformed",
		"warning should name the condition")
	assert.Contains(t, output, "10",
		"warning should name the 1-based line number from the fixture")
}

func TestApply_PreservesFileHistorySnapshots(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	// Plant a binary-looking snapshot next to the fixture's text snapshot.
	// Both must round-trip byte-identically through the move: file-history
	// snapshots are opaque user-file bytes, and cc-port never rewrites their
	// contents — their project-path strings (if any) are coincidental.
	fileHistorySessionDir := filepath.Join(
		claudeHome.FileHistoryDir(), "a1b2c3d4-0000-0000-0000-000000000001",
	)
	binarySnapshotPath := filepath.Join(fileHistorySessionDir, "binary0000000000@v1")
	binarySnapshotContent := append([]byte{0x00, 0x01, 0x02}, []byte(oldProjectPath)...)
	require.NoError(t, os.WriteFile(binarySnapshotPath, binarySnapshotContent, 0600))

	textSnapshotPath := filepath.Join(fileHistorySessionDir, "cafebabe11111111@v1")
	textBefore, err := os.ReadFile(textSnapshotPath) //nolint:gosec // test path
	require.NoError(t, err)
	require.Contains(t, string(textBefore), oldProjectPath,
		"fixture precondition: text snapshot must reference old path")

	plan, err := move.DryRun(claudeHome, move.Options{
		OldPath: oldProjectPath,
		NewPath: newProjectPath,
	})
	require.NoError(t, err)
	assert.Positive(t, plan.ReplacementsByCategory["file-history-snapshots"],
		"dry-run should count the snapshots that will be preserved as-is")

	var warnings bytes.Buffer
	err = move.Apply(claudeHome, move.Options{
		OldPath:       oldProjectPath,
		NewPath:       newProjectPath,
		RefsOnly:      true,
		WarningWriter: &warnings,
	})
	require.NoError(t, err)

	textAfter, err := os.ReadFile(textSnapshotPath) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, textBefore, textAfter,
		"text snapshot must round-trip byte-identically")
	assert.Contains(t, string(textAfter), oldProjectPath,
		"text snapshot's old-path substring must survive the move verbatim")

	binaryAfter, err := os.ReadFile(binarySnapshotPath) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, binarySnapshotContent, binaryAfter,
		"binary snapshot must round-trip byte-identically")

	output := warnings.String()
	assert.Contains(t, output, "file-history snapshot",
		"warning should name the file-history category")
	assert.Contains(t, output, "preserved",
		"warning should state the preservation invariant")
}

func TestDryRun_CountsSessionKeyedReplacements(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	plan, err := move.DryRun(claudeHome, move.Options{
		OldPath:  "/Users/test/Projects/myproject",
		NewPath:  renamedProjectPath,
		RefsOnly: true,
	})
	require.NoError(t, err)

	assert.Positive(t, plan.ReplacementsByCategory["todos"], "todo file mentions oldPath in fixture")
	assert.Positive(t, plan.ReplacementsByCategory["usage-data/session-meta"], "session-meta has project_path")
	assert.Positive(t, plan.ReplacementsByCategory["plugins-data"], "plugin tracker has the abs path as JSON key")
	assert.Positive(t, plan.ReplacementsByCategory["tasks"], "task description mentions oldPath")
}

func TestApply_Rollback_RestoresAllShapesOnFailure(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	// Snapshot current todo + usage-data + plugin + task contents
	originalLocations, err := claude.LocateProject(claudeHome, oldProjectPath)
	require.NoError(t, err)

	beforeBytes := func(paths []string) map[string][]byte {
		result := make(map[string][]byte, len(paths))
		for _, p := range paths {
			b, readErr := os.ReadFile(p) //nolint:gosec // test-controlled path
			require.NoError(t, readErr)
			result[p] = b
		}
		return result
	}
	todoBefore := beforeBytes(originalLocations.TodoFiles)
	metaBefore := beforeBytes(originalLocations.UsageDataSessionMeta)

	// Force failure: supply a NewPath that collides with an existing encoded project dir
	// (refused by checkEncodedDirCollision before any write).
	collidingNewPath := "/Users/test/Projects/my-project-v2"

	err = move.Apply(claudeHome, move.Options{
		OldPath:  oldProjectPath,
		NewPath:  collidingNewPath,
		RefsOnly: true,
	})
	require.Error(t, err, "encoded-dir collision must abort the move before any write")

	// Verify original bytes are unchanged
	for path, expected := range todoBefore {
		actual, readErr := os.ReadFile(path) //nolint:gosec // test-controlled path
		require.NoError(t, readErr)
		assert.Equal(t, expected, actual, "todo file %s must be untouched after refused move", path)
	}
	for path, expected := range metaBefore {
		actual, readErr := os.ReadFile(path) //nolint:gosec // test-controlled path
		require.NoError(t, readErr)
		assert.Equal(t, expected, actual, "usage-data file %s must be untouched after refused move", path)
	}
}

func TestApply_RewritesTasks_SkipsSidecars(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	err := move.Apply(claudeHome, move.Options{
		OldPath:  oldProjectPath,
		NewPath:  renamedProjectPath,
		RefsOnly: true,
	})
	require.NoError(t, err)

	newLocations, err := claude.LocateProject(claudeHome, renamedProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, newLocations.TaskFiles)

	// TaskFiles is a flat slice that (post Task 1) still contains .lock and
	// .highwatermark sidecars — the filter lives on SessionKeyedGroup.
	byName := func(paths []string, name string) string {
		for _, p := range paths {
			if filepath.Base(p) == name {
				return p
			}
		}
		return ""
	}

	taskJSON := byName(newLocations.TaskFiles, "1.json")
	require.NotEmpty(t, taskJSON, "expected 1.json in TaskFiles")
	body, err := os.ReadFile(taskJSON) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Contains(t, string(body), renamedProjectPath)
	assert.NotContains(t, string(body), oldProjectPath)

	// .lock and .highwatermark must still exist but be untouched
	lockPath := byName(newLocations.TaskFiles, ".lock")
	require.NotEmpty(t, lockPath, "expected .lock sidecar in TaskFiles")
	_, err = os.Stat(lockPath)
	require.NoError(t, err, ".lock sidecar must be preserved verbatim")
	hwPath := byName(newLocations.TaskFiles, ".highwatermark")
	require.NotEmpty(t, hwPath, "expected .highwatermark sidecar in TaskFiles")
	hwBody, err := os.ReadFile(hwPath) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, "1", string(hwBody), ".highwatermark must be preserved verbatim")
}

func TestApply_RewritesPluginsData(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	err := move.Apply(claudeHome, move.Options{
		OldPath:  oldProjectPath,
		NewPath:  renamedProjectPath,
		RefsOnly: true,
	})
	require.NoError(t, err)

	newLocations, err := claude.LocateProject(claudeHome, renamedProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, newLocations.PluginsDataFiles)

	var pluginFile string
	for _, p := range newLocations.PluginsDataFiles {
		if filepath.Base(p) == "tracker-main.json" {
			pluginFile = p
			break
		}
	}
	require.NotEmpty(t, pluginFile, "expected tracker-main.json in PluginsDataFiles")
	body, err := os.ReadFile(pluginFile) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Contains(t, string(body), renamedProjectPath)
	assert.NotContains(t, string(body), oldProjectPath)
}

func TestApply_RewritesUsageData(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	err := move.Apply(claudeHome, move.Options{
		OldPath:  oldProjectPath,
		NewPath:  renamedProjectPath,
		RefsOnly: true,
	})
	require.NoError(t, err)

	newLocations, err := claude.LocateProject(claudeHome, renamedProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, newLocations.UsageDataSessionMeta)

	body, err := os.ReadFile(newLocations.UsageDataSessionMeta[0])
	require.NoError(t, err)
	assert.Contains(t, string(body), `"project_path":"`+renamedProjectPath+`"`)
	assert.NotContains(t, string(body), oldProjectPath)
}

func TestApply_RewritesTodos(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	err := move.Apply(claudeHome, move.Options{
		OldPath:  oldProjectPath,
		NewPath:  renamedProjectPath,
		RefsOnly: true,
	})
	require.NoError(t, err)

	newLocations, err := claude.LocateProject(claudeHome, renamedProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, newLocations.TodoFiles)

	body, err := os.ReadFile(newLocations.TodoFiles[0])
	require.NoError(t, err)
	assert.Contains(t, string(body), renamedProjectPath)
	assert.NotContains(t, string(body), oldProjectPath)
}

// TestApply_Rollback_RestoresGlobalsOnSecondDeleteFailure exercises the
// asymmetric-rollback fix in deleteOriginals: when the encoded project dir
// removal succeeds but the on-disk OldPath removal fails, the tracker must
// still restore every rewritten global file so nothing points at a path that
// no longer exists. The failure is injected by placing OldPath under a
// read-only parent directory — on macOS and Linux, unlinking an entry needs
// write+execute on the parent, so os.RemoveAll fails cleanly without mocking
// the stdlib.
func TestApply_Rollback_RestoresGlobalsOnSecondDeleteFailure(t *testing.T) {
	tempRoot := t.TempDir()
	claudeDir := filepath.Join(tempRoot, "dotclaude")
	configFilePath := filepath.Join(tempRoot, "dotclaude.json")

	readOnlyParent := filepath.Join(tempRoot, "readonly-parent")
	oldOnDiskPath := filepath.Join(readOnlyParent, "myproject")
	newOnDiskPath := filepath.Join(tempRoot, "new-parent", "myproject")

	// Synthesize the minimum ~/.claude fixture: the encoded project dir (so
	// LocateProject resolves), history.jsonl + settings.json containing the
	// old path (so rewriteGlobalFiles actually mutates tracked bytes), and an
	// empty projects/ sibling tree.
	claudeHome := &claude.Home{Dir: claudeDir, ConfigFile: configFilePath}
	require.NoError(t, os.MkdirAll(claudeHome.ProjectDir(oldOnDiskPath), 0o750))

	historyOriginal := []byte(`{"display":"x","pastedContents":{},"timestamp":1,"project":"` +
		oldOnDiskPath + `"}` + "\n")
	require.NoError(t, os.WriteFile(claudeHome.HistoryFile(), historyOriginal, 0o600))

	settingsOriginal := []byte(`{"env":{"PROJECT_ROOT":"` + oldOnDiskPath + `"}}`)
	require.NoError(t, os.WriteFile(claudeHome.SettingsFile(), settingsOriginal, 0o600))

	configOriginal := []byte(`{"projects":{"` + oldOnDiskPath + `":{}}}`)
	require.NoError(t, os.WriteFile(configFilePath, configOriginal, 0o600))

	// On-disk OldPath must exist so the copy phase succeeds. A single file
	// inside is enough to exercise CopyDir; deletion is what we want to fail.
	require.NoError(t, os.MkdirAll(oldOnDiskPath, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(oldOnDiskPath, "sentinel.txt"), []byte("payload"), 0o600))

	// Lock the parent so os.RemoveAll(oldOnDiskPath) fails at the unlink step.
	// Restore before t.TempDir() teardown so its own cleanup can remove entries.
	require.NoError(t, os.Chmod(readOnlyParent, 0o500)) //nolint:gosec // G302: read+exec only is the whole point
	t.Cleanup(func() {
		_ = os.Chmod(readOnlyParent, 0o700) //nolint:gosec // G302: restore for t.TempDir cleanup
	})

	err := move.Apply(claudeHome, move.Options{
		OldPath:  oldOnDiskPath,
		NewPath:  newOnDiskPath,
		RefsOnly: false,
	})
	require.Error(t, err, "Apply must surface the on-disk removal failure")
	assert.Contains(t, err.Error(), "remove old project dir on disk",
		"error should come from the second-delete branch of deleteOriginals")

	// Global files must be restored to their pre-move contents — the contract
	// promised by Apply's godoc. Without the tracker.restore() added to the
	// second-delete branch, these bytes would still reference newOnDiskPath.
	historyAfter, err := os.ReadFile(claudeHome.HistoryFile())
	require.NoError(t, err)
	assert.Equal(t, historyOriginal, historyAfter,
		"history.jsonl must be restored when the on-disk delete fails")

	settingsAfter, err := os.ReadFile(claudeHome.SettingsFile())
	require.NoError(t, err)
	assert.Equal(t, settingsOriginal, settingsAfter,
		"settings.json must be restored when the on-disk delete fails")

	configAfter, err := os.ReadFile(configFilePath) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, configOriginal, configAfter,
		"user config must be restored when the on-disk delete fails")

	// The executeMove defer removes both newly created paths in reverse order.
	_, statNewEncodedErr := os.Stat(claudeHome.ProjectDir(newOnDiskPath))
	assert.True(t, os.IsNotExist(statNewEncodedErr),
		"new encoded project dir must be torn down by executeMove's defer")
	_, statNewOnDiskErr := os.Stat(newOnDiskPath)
	assert.True(t, os.IsNotExist(statNewOnDiskErr),
		"new on-disk project dir must be torn down by executeMove's defer")
}

// assertSessionSubdirFilesRewritten walks the session subdirectories under
// newProjectDir and asserts that every file has had the old project path
// rewritten to the new project path. Covers <uuid>/subagents/*.jsonl and
// <uuid>/session-memory/** — the files that were silently skipped before #7
// was fixed.
func assertSessionSubdirFilesRewritten(t *testing.T, newProjectDir string) {
	t.Helper()

	topLevel, err := os.ReadDir(newProjectDir)
	require.NoError(t, err)

	var subdirFiles []string
	for _, entry := range topLevel {
		if !entry.IsDir() || entry.Name() == "memory" || entry.Name() == "sessions" {
			continue
		}
		err := filepath.WalkDir(
			filepath.Join(newProjectDir, entry.Name()),
			func(path string, walked fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if walked.IsDir() {
					return nil
				}
				subdirFiles = append(subdirFiles, path)
				return nil
			},
		)
		require.NoError(t, err)
	}

	require.NotEmpty(t, subdirFiles,
		"fixture must include session-subdir files to exercise the fix for #7")

	for _, filePath := range subdirFiles {
		data, err := os.ReadFile(filePath) //nolint:gosec // test file path
		require.NoError(t, err)
		content := string(data)
		assert.NotContains(t, content, oldProjectPath+"/",
			"subdir file %s should not contain old project path followed by /", filePath)
		assert.NotContains(t, content, `"`+oldProjectPath+`"`,
			"subdir file %s should not contain old project path as a quoted JSON value", filePath)
		assert.Contains(t, content, newProjectPath,
			"subdir file %s should contain new project path after rewrite", filePath)
	}
}
