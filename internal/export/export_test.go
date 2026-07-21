package export_test

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func fixtureTargets(t *testing.T) (targets []tool.Target, projectPath string) {
	t.Helper()
	home := testutil.SetupFixture(t)
	claudeTool := claude.New()
	return []tool.Target{{Tool: claudeTool, Workspace: claude.NewWorkspace(home)}}, testutil.FixtureProjectPath()
}

func allSelected(t tool.Tool) map[string]bool {
	selected := make(map[string]bool)
	for _, category := range t.Categories() {
		selected[category.Name] = true
	}
	return selected
}

func TestRun_WritesClaudePrefixedEntriesAndMetadata(t *testing.T) {
	targets, projectPath := fixtureTargets(t)

	var buf bytes.Buffer
	result, err := export.Run(context.Background(), targets, &export.Options{
		ProjectPath: projectPath,
		Output:      &buf,
		Selected:    map[string]map[string]bool{"claude": allSelected(targets[0].Tool)},
	})
	require.NoError(t, err)
	assert.Equal(t, "metadata.xml", result.Metadata.Name)

	claudeResult, ok := result.ByTool["claude"]
	require.True(t, ok)
	assert.NotEmpty(t, claudeResult.Categories["sessions"])

	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)

	sawMetadata := false
	for _, file := range zipReader.File {
		if file.Name == "metadata.xml" {
			sawMetadata = true
			continue
		}
		assert.Truef(t, len(file.Name) > len("claude/") && file.Name[:len("claude/")] == "claude/",
			"entry %q must carry the claude/ tool prefix", file.Name)
	}
	assert.True(t, sawMetadata, "archive must contain metadata.xml at the root")
}

func TestRun_ProjectAbsentWritesEmptyToolBlock(t *testing.T) {
	targets, _ := fixtureTargets(t)

	var buf bytes.Buffer
	result, err := export.Run(context.Background(), targets, &export.Options{
		ProjectPath: "/no/such/project",
		Output:      &buf,
		Selected:    map[string]map[string]bool{"claude": allSelected(targets[0].Tool)},
	})
	require.NoError(t, err, "a project unknown to every target must not fail the export")

	claudeResult := result.ByTool["claude"]
	assert.Empty(t, claudeResult.Categories["sessions"])
}
