package claude_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/testutil"
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

	// Shape checks: required artefacts must be present, but we do not pin the
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

	projectLocations, err := claude.LocateProject(claudeHome, "/nonexistent/project/path")
	require.Error(t, err)
	assert.Nil(t, projectLocations)
}
