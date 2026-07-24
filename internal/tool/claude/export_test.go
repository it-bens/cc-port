package claude

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
)

// VerifyProjectIdentityForTest exposes verifyProjectIdentity so fuzz tests in
// package claude_test can exercise the guard without routing through
// LocateProject. Production code continues to reach the guard only via
// LocateProject.
var VerifyProjectIdentityForTest = verifyProjectIdentity

func TestExport_RendersRulesFileWarnings(t *testing.T) {
	projectPath := "/Users/test/Projects/myproject"
	home := &Home{Dir: t.TempDir()}
	require.NoError(t, os.MkdirAll(home.ProjectDir(projectPath), 0o750))
	require.NoError(t, os.MkdirAll(home.RulesDir(), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(home.RulesDir(), "test-rule.md"),
		[]byte("# Rule\n\nThis rule applies to /Users/test/Projects/myproject only.\n"),
		0o600,
	))
	workspace := NewWorkspace(home)
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	sink := archive.NewSink(writer, "claude", nil)

	result, err := workspace.Export(context.Background(), projectPath, map[string]bool{}, sink)

	require.NoError(t, err)
	require.NoError(t, writer.Close())
	assert.Contains(t, result.Warnings, "rules file test-rule.md (line 3) references this project")
}

// newConfigExportWorkspace stages a home whose config file carries one
// project block and returns the workspace for config-export tests.
func newConfigExportWorkspace(t *testing.T, projectPath, projectBlock string) *Workspace {
	t.Helper()
	dir := t.TempDir()
	home := &Home{Dir: filepath.Join(dir, "dotclaude"), ConfigFile: filepath.Join(dir, "dotclaude.json")}
	require.NoError(t, os.MkdirAll(home.ProjectDir(projectPath), 0o750))
	config := `{"projects":{"` + projectPath + `":` + projectBlock + `}}`
	require.NoError(t, os.WriteFile(home.ConfigFile, []byte(config), 0o600))
	return NewWorkspace(home)
}

// exportToZipEntries runs Export with selected and returns the archive's
// entries as name -> content.
func exportToZipEntries(t *testing.T, workspace *Workspace, projectPath string, selected map[string]bool) map[string]string {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	sink := archive.NewSink(writer, "claude", nil)
	_, err := workspace.Export(context.Background(), projectPath, selected, sink)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	reader, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	require.NoError(t, err)
	entries := make(map[string]string, len(reader.File))
	for _, file := range reader.File {
		readCloser, err := file.Open()
		require.NoError(t, err)
		content, err := io.ReadAll(readCloser)
		require.NoError(t, err)
		require.NoError(t, readCloser.Close())
		entries[file.Name] = string(content)
	}
	return entries
}

func TestExport_SplitsAllowedToolsIntoConfigGrantsEntry(t *testing.T) {
	projectPath := "/Users/test/Projects/myproject"
	workspace := newConfigExportWorkspace(t, projectPath, `{"allowedTools":["Bash(go:*)"],"setting":"kept"}`)

	entries := exportToZipEntries(t, workspace, projectPath,
		map[string]bool{categoryConfig: true, categoryConfigGrants: true})

	assert.JSONEq(t, `{"setting":"kept"}`, entries["claude/config.json"],
		"the config entry must not carry allowedTools")
	assert.JSONEq(t, `{"allowedTools":["Bash(go:*)"]}`, entries["claude/config-grants.json"],
		"the grants entry must carry allowedTools as its single key")
}

func TestExport_ConfigOnlySelectionWritesNoGrantsEntry(t *testing.T) {
	projectPath := "/Users/test/Projects/myproject"
	workspace := newConfigExportWorkspace(t, projectPath, `{"allowedTools":["Bash(go:*)"],"setting":"kept"}`)

	entries := exportToZipEntries(t, workspace, projectPath, map[string]bool{categoryConfig: true})

	assert.NotContains(t, entries, "claude/config-grants.json",
		"an unselected grants category must keep allowedTools out of the archive entirely")
	assert.JSONEq(t, `{"setting":"kept"}`, entries["claude/config.json"])
}

func TestExport_ConfigGrantsWithoutAllowedToolsWritesEmptyBlock(t *testing.T) {
	projectPath := "/Users/test/Projects/myproject"
	workspace := newConfigExportWorkspace(t, projectPath, `{"setting":"kept"}`)

	entries := exportToZipEntries(t, workspace, projectPath, map[string]bool{categoryConfigGrants: true})

	assert.JSONEq(t, `{}`, entries["claude/config-grants.json"],
		"entry presence tracks category selection, so a grantless project exports an empty block")
}
