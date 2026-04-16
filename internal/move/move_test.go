package move_test

import (
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
	oldProjectPath = "/Users/test/Projects/myproject"
	newProjectPath = "/Users/test/Projects/newproject"
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

	assert.Positive(t, plan.SessionsIndexReplacements, "expected sessions-index replacements")
	assert.Positive(t, plan.HistoryReplacements, "expected history replacements")
	assert.Positive(t, plan.SessionFileReplacements, "expected session file replacements")
	assert.Positive(t, plan.SettingsReplacements, "expected settings replacements")

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

	// sessions-index.json should reference the new path.
	sessionsIndexPath := filepath.Join(newProjectDir, "sessions-index.json")
	sessionsIndexData, err := os.ReadFile(sessionsIndexPath) //nolint:gosec // test file path
	require.NoError(t, err)

	var sessionsIndex claude.SessionsIndex
	require.NoError(t, json.Unmarshal(sessionsIndexData, &sessionsIndex))
	for _, entry := range sessionsIndex.Entries {
		assert.Equal(t, newProjectPath, entry.ProjectPath,
			"sessions-index entry should have new project path")
		assert.NotContains(t, entry.ProjectPath, oldProjectPath,
			"sessions-index entry should not contain old project path")
	}

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

	// New project data dir should exist.
	newProjectDir := claudeHome.ProjectDir(newProjectPath)
	_, err = os.Stat(newProjectDir)
	require.NoError(t, err, "new project data dir should exist")

	// OldPath disk directory should NOT have been created (it doesn't exist in fixture).
	// With RefsOnly=true, we simply don't touch the actual project directory on disk.
	// We verify the new encoded dir was created by checking its contents.
	sessionsIndexPath := filepath.Join(newProjectDir, "sessions-index.json")
	_, err = os.Stat(sessionsIndexPath)
	require.NoError(t, err, "sessions-index.json should exist in new project data dir")
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
