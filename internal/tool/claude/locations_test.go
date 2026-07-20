package claude_test

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

const testProjectPath = "/Users/test/Projects/myproject"

// containsBaseName reports whether any path in items has the given base name.
func containsBaseName(items []string, baseName string) bool {
	for _, item := range items {
		if filepath.Base(item) == baseName {
			return true
		}
	}
	return false
}

func TestLocateProject(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	projectLocations, err := claude.LocateProject(claudeHome, testProjectPath)
	require.NoError(t, err)
	require.NotNil(t, projectLocations)

	assert.Equal(t, testProjectPath, projectLocations.ProjectPath)
	assert.NotEmpty(t, projectLocations.ProjectDir)

	// Shape checks: required artifacts must be present, but we do not pin the
	// exact count so the fixture can grow without invalidating these tests.
	assert.NotEmpty(t, projectLocations.SessionTranscripts, "expected at least one transcript .jsonl file")
	assert.True(t,
		containsBaseName(projectLocations.SessionTranscripts, "a1b2c3d4-0000-0000-0000-000000000001.jsonl"),
		"primary transcript must be located")

	assert.NotEmpty(t, projectLocations.MemoryFiles, "expected at least one memory file")
	assert.True(t, containsBaseName(projectLocations.MemoryFiles, "MEMORY.md"), "MEMORY.md must be located")
	assert.True(t,
		containsBaseName(projectLocations.MemoryFiles, "project_notes.md"),
		"project_notes.md must be located")

	assert.Positive(t, projectLocations.HistoryEntryCount, "expected at least one history entry")

	assert.NotEmpty(t, projectLocations.SessionFiles, "expected at least one session file matching cwd")
	assert.True(t, containsBaseName(projectLocations.SessionFiles, "99999.json"), "session 99999.json must match")

	assert.NotEmpty(t, projectLocations.FileHistoryDirs, "expected at least one file-history dir")
	assert.True(t,
		containsBaseName(projectLocations.FileHistoryDirs, "a1b2c3d4-0000-0000-0000-000000000001"),
		"primary session's file-history dir must be located")

	assert.True(t, projectLocations.HasConfigBlock, "expected project to have a config block in .claude.json")
}

func TestLocateProject_NotFound(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	absentProjectPath := filepath.Join(t.TempDir(), "absent-project")
	projectLocations, err := claude.LocateProject(claudeHome, absentProjectPath)
	require.Error(t, err)
	assert.Nil(t, projectLocations)
}

func TestLocateProject_CollectsUsageData(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	projectLocations, err := claude.LocateProject(claudeHome, testProjectPath)
	require.NoError(t, err)

	assert.Len(t, projectLocations.UsageDataSessionMeta, 1)
	assert.True(t,
		containsBaseName(projectLocations.UsageDataSessionMeta,
			"a1b2c3d4-0000-0000-0000-000000000001.json"),
		"session-meta JSON for primary session UUID must be located")

	assert.Len(t, projectLocations.UsageDataFacets, 1)
	assert.True(t,
		containsBaseName(projectLocations.UsageDataFacets,
			"a1b2c3d4-0000-0000-0000-000000000001.json"),
		"facets JSON for primary session UUID must be located")
}

func TestLocateProject_CollectsPluginsData(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	projectLocations, err := claude.LocateProject(claudeHome, testProjectPath)
	require.NoError(t, err)

	assert.Len(t, projectLocations.PluginsDataFiles, 1)
	assert.True(t,
		containsBaseName(projectLocations.PluginsDataFiles, "tracker-main.json"),
		"plugin-namespace session file for primary UUID must be located")
}

func TestLocateProject_CollectsTaskFiles(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	projectLocations, err := claude.LocateProject(claudeHome, testProjectPath)
	require.NoError(t, err)

	assert.Len(t, projectLocations.TaskFiles, 3,
		"task subtree file enumeration includes .lock and .highwatermark sidecars")
	assert.True(t, containsBaseName(projectLocations.TaskFiles, "1.json"),
		"task JSON file for primary session UUID must be located")
	assert.True(t, containsBaseName(projectLocations.TaskFiles, ".lock"),
		".lock sidecar must be included at this layer (filter lives in registry)")
	assert.True(t, containsBaseName(projectLocations.TaskFiles, ".highwatermark"),
		".highwatermark sidecar must be included at this layer (filter lives in registry)")
}

func TestLocateProject_CollectsTodos(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	projectLocations, err := claude.LocateProject(claudeHome, testProjectPath)
	require.NoError(t, err)

	assert.Len(t, projectLocations.TodoFiles, 1, "exactly one todo file matches a project session UUID")
	assert.True(t,
		containsBaseName(projectLocations.TodoFiles,
			"a1b2c3d4-0000-0000-0000-000000000001-agent-a1b2c3d4-0000-0000-0000-000000000001.json"),
		"the matching todo file must be located")
}

func TestCollectMemoryFiles_ExcludesRollbackArtifact(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	memoryDir := filepath.Join(claudeHome.ProjectDir(testProjectPath), "memory")
	artifactName := "MEMORY.md" + rewrite.RollbackSuffix
	require.NoError(t, os.WriteFile(filepath.Join(memoryDir, artifactName), []byte("stale rollback content"), 0o600))

	projectLocations, err := claude.LocateProject(claudeHome, testProjectPath)

	require.NoError(t, err)
	assert.False(t, containsBaseName(projectLocations.MemoryFiles, artifactName),
		"memory discovery must ignore cc-port's own rollback artifacts")
	assert.True(t, containsBaseName(projectLocations.MemoryFiles, "MEMORY.md"),
		"the real memory file must still be located")
}

func TestLocateProject_ExcludesArtifactsFromPluginsDataAndTasks(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	workspace := claude.NewWorkspace(claudeHome)
	locations, err := claude.LocateProject(claudeHome, testProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, locations.PluginsDataFiles)
	require.NotEmpty(t, locations.TaskFiles)
	beforeCounts, err := workspace.ReferenceSurfaces(t.Context(), testProjectPath)
	require.NoError(t, err)

	// artifactBaseNames covers all four IsArtifactPath classes: the three
	// whole-name suffixes, plus SafeWriteTempPrefix's leading prefix. The
	// prefix class is the one that a full-path (rather than base-name) match
	// against IsArtifactPath fails to filter, since strings.HasPrefix never
	// matches past a leading directory component.
	artifactBaseNames := []string{
		"stale.json" + rewrite.RollbackSuffix,
		"stale.json" + rewrite.ImportStagingSuffix,
		"stale.json" + rewrite.StagingSuffix,
		rewrite.SafeWriteTempPrefix + "stale123",
	}
	pluginDir := filepath.Dir(locations.PluginsDataFiles[0])
	taskDir := filepath.Dir(locations.TaskFiles[0])
	var pluginArtifacts, taskArtifacts []string
	for _, baseName := range artifactBaseNames {
		pluginArtifact := filepath.Join(pluginDir, baseName)
		taskArtifact := filepath.Join(taskDir, baseName)
		require.NoError(t, os.WriteFile(pluginArtifact, []byte(testProjectPath), 0o600))
		require.NoError(t, os.WriteFile(taskArtifact, []byte(testProjectPath), 0o600))
		pluginArtifacts = append(pluginArtifacts, pluginArtifact)
		taskArtifacts = append(taskArtifacts, taskArtifact)
	}

	locations, err = claude.LocateProject(claudeHome, testProjectPath)
	require.NoError(t, err)
	for _, artifact := range pluginArtifacts {
		baseName := filepath.Base(artifact)
		assert.False(t, containsBaseName(locations.PluginsDataFiles, baseName), "plugins-data artifact %s must be excluded", baseName)
	}
	for _, artifact := range taskArtifacts {
		baseName := filepath.Base(artifact)
		assert.False(t, containsBaseName(locations.TaskFiles, baseName), "tasks artifact %s must be excluded", baseName)
	}
	assert.True(t, containsBaseName(locations.PluginsDataFiles, "tracker-main.json"))
	assert.True(t, containsBaseName(locations.TaskFiles, "1.json"))

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	selected := map[string]bool{"plugins-data": true, "tasks": true}
	result, err := workspace.Export(t.Context(), testProjectPath, selected, archive.NewSink(writer, "claude", nil))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	allArtifacts := append(append([]string{}, pluginArtifacts...), taskArtifacts...)
	for _, entry := range append(result.Categories["plugins-data"], result.Categories["tasks"]...) {
		for _, artifact := range allArtifacts {
			assert.NotContains(t, entry.ArchivePath, filepath.Base(artifact))
		}
	}

	counts, err := workspace.ReferenceSurfaces(t.Context(), testProjectPath)
	require.NoError(t, err)
	beforeByName := make(map[string]int, len(beforeCounts))
	for _, count := range beforeCounts {
		beforeByName[count.Name] = count.Count
	}
	for _, count := range counts {
		if count.Name == "plugins-data" || count.Name == "tasks" {
			assert.Equal(t, beforeByName[count.Name], count.Count, "artifact-only path references must not contribute to stats")
		}
	}
}

func TestLocateProject_RefusesEncodedDirWithMismatchedSessionCwd(t *testing.T) {
	// Fixture collision: "/Users/test/Projects/my project" (real cwd in
	// sessions/12345.json) and "/Users/test/Projects/my-project" (the lookup
	// path) both encode to -Users-test-Projects-my-project. The witnessing
	// session's sessionId e5f6a7b8-0000-0000-0000-000000000005 appears in
	// the encoded dir as a transcript, so the identity check sees the
	// mismatched cwd and must refuse.
	claudeHome := testutil.SetupFixture(t)

	_, err := claude.LocateProject(claudeHome, "/Users/test/Projects/my-project")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to rewrite")
	assert.Contains(t, err.Error(), `"/Users/test/Projects/my project"`,
		"error must name the witness cwd so the operator can identify the colliding project")
}

func TestLocateProject_PassesOnMatchingSessionCwd(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	locations, err := claude.LocateProject(claudeHome, testProjectPath)

	require.NoError(t, err)
	assert.Equal(t, testProjectPath, locations.ProjectPath)
}

func TestLocateProject_EmitsWarningOnNoSessionFiles(t *testing.T) {
	// Arrange: wipe every session JSON so no witness exists for the encoded dir.
	claudeHome := testutil.SetupFixture(t)
	entries, err := os.ReadDir(claudeHome.SessionsDir())
	require.NoError(t, err)
	for _, entry := range entries {
		require.NoError(t, os.Remove(filepath.Join(claudeHome.SessionsDir(), entry.Name())))
	}

	originalStderr := os.Stderr
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = writer
	t.Cleanup(func() { os.Stderr = originalStderr })

	// Act
	_, err = claude.LocateProject(claudeHome, testProjectPath)
	_ = writer.Close()

	// Assert
	require.NoError(t, err)
	var buffer bytes.Buffer
	_, _ = buffer.ReadFrom(reader)
	assert.Contains(t, buffer.String(), "identity check skipped")
}
