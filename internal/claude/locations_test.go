package claude_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/testutil"
)

const testProjectPath = "/Users/test/Projects/myproject"

func TestLocateProject(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	projectLocations, err := claude.LocateProject(claudeHome, testProjectPath)
	require.NoError(t, err)
	require.NotNil(t, projectLocations)

	assert.Equal(t, testProjectPath, projectLocations.ProjectPath)
	assert.NotEmpty(t, projectLocations.ProjectDir)

	assert.NotEmpty(t, projectLocations.SessionsIndex, "sessions-index.json should be found")

	assert.Len(t, projectLocations.SessionTranscripts, 1, "expected 1 transcript .jsonl file")
	assert.Len(t, projectLocations.MemoryFiles, 2, "expected 2 memory files")
	assert.Equal(t, 2, projectLocations.HistoryEntryCount, "expected 2 history entries for this project")
	assert.Len(t, projectLocations.SessionFiles, 1, "expected 1 session file matching cwd")
	assert.Len(t, projectLocations.FileHistoryDirs, 1, "expected 1 file-history dir matching session UUID")
	assert.True(t, projectLocations.HasConfigBlock, "expected project to have a config block in .claude.json")
}

func TestLocateProject_NotFound(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	projectLocations, err := claude.LocateProject(claudeHome, "/nonexistent/project/path")
	require.Error(t, err)
	assert.Nil(t, projectLocations)
}
