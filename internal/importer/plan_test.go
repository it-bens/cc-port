package importer_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func TestDryRun_LeavesTheDestinationUntouched(t *testing.T) {
	body, projectPath := buildArchive(t)
	home := blankHome(t)
	before := treeSnapshot(t, filepath.Dir(home.Dir))

	_, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(home), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	assert.Equal(t, before, treeSnapshot(t, filepath.Dir(home.Dir)),
		"a plan must write nothing, including staging temps")
}

func TestDryRun_ReportsWhatEachToolsArchiveBlockHolds(t *testing.T) {
	body, projectPath := buildArchive(t)

	plan, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(blankHome(t)), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	require.Len(t, plan.Tools, 1)
	assert.Equal(t, projectPath, plan.TargetPath)
	assert.Equal(t, "claude", plan.Tools[0].Name)
	assert.Contains(t, plan.Tools[0].Categories, "sessions")
	assert.Positive(t, plan.Tools[0].Entries)
}

// The rendered plan is what an operator reads before passing --apply, so it
// must carry the plan's own data rather than a header alone.
func TestPlanRender_ShowsWhatAnApplyWouldCommit(t *testing.T) {
	body, projectPath := buildArchive(t)
	plan, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(blankHome(t)), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	var out bytes.Buffer
	require.NoError(t, plan.Render(&out))

	rendered := out.String()
	assert.Contains(t, rendered, "Target: "+projectPath)
	assert.Contains(t, rendered, "[claude]")
	assert.Regexp(t, `Categories:.*\bsessions\b`, rendered)
	assert.Regexp(t, `Entries:\s+\d+`, rendered)
	assert.Regexp(t, `\bfs\s+node `+regexp.QuoteMeta(projectPath), rendered)
	assert.Contains(t, rendered, "Run with --apply to execute.")
}

// The archive's project block carries the fixture project's own mcpServers.
// Those launch with any session opened on the imported project, so a
// destination that declares none must see them listed before it applies.
func TestDryRun_ReportsProjectBlockMCPServersAbsentFromTheDestination(t *testing.T) {
	body, projectPath := buildArchive(t)

	plan, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(blankHome(t)), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	require.Len(t, plan.NewMCPServers, 1)
	assert.Equal(t, "claude", plan.NewMCPServers[0].Tool)
	require.Len(t, plan.NewMCPServers[0].Servers, 1)
	assert.Equal(t, "fs", plan.NewMCPServers[0].Servers[0].Name)
	assert.Equal(t, "node", plan.NewMCPServers[0].Servers[0].Command)
}

func TestDryRun_OmitsMCPServersTheDestinationAlreadyDeclares(t *testing.T) {
	body, projectPath := buildArchive(t)
	home := blankHome(t)
	// Byte-identical to what the archive's project block resolves to on this
	// destination, so the omission cannot be read as a near-match slipping
	// through.
	require.NoError(t, os.WriteFile(home.ConfigFile, []byte(
		`{"mcpServers":{"fs":{"command":"node","args":["`+projectPath+`/mcp/fs-server.js"]}},"projects":{}}`,
	), 0o600))

	plan, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(home), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})
	require.NoError(t, err)

	assert.Empty(t, plan.NewMCPServers,
		"a definition the destination already declares under the same name is not newly arriving")
}

func TestDryRun_FailsWhenTheDestinationConfigCannotBeRead(t *testing.T) {
	body, projectPath := buildArchive(t)
	home := blankHome(t)
	require.NoError(t, os.WriteFile(home.ConfigFile, []byte(`{"mcpServers":`), 0o600))

	_, err := importer.DryRun(t.Context(), claudeToolSet(), claudeTargets(home), &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Caps:       archive.DefaultCaps(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read destination MCP servers for claude")
}

func TestRenderMCPServers_NamesEachDefinitionWithItsLaunchLine(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, importer.RenderMCPServers(&out, []importer.MCPServerSet{{
		Tool: "claude",
		Servers: []tool.MCPServer{
			{Name: "filesystem", Command: "node", Args: []string{"--enable-source-maps", "/opt/fs-server.js"}},
			{Name: "search", URL: "https://mcp.example.invalid/search"},
		},
	}}))

	rendered := out.String()
	assert.Contains(t, rendered, "New MCP servers (claude):")
	assert.Regexp(t, `filesystem\s+node --enable-source-maps /opt/fs-server\.js`, rendered)
	assert.Regexp(t, `search\s+https://mcp\.example\.invalid/search`, rendered)
	assert.Contains(t, rendered, "start automatically with every session once imported")
}

func TestRenderMCPServers_WritesNothingWithoutNewDefinitions(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, importer.RenderMCPServers(&out, nil))

	assert.Empty(t, out.String())
}

func claudeToolSet() *tool.Set { return tool.NewSet(claude.New()) }

func claudeTargets(home *claude.Home) []tool.Target {
	return []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
}

// treeSnapshot lists every path under root with a digest of its contents, so a
// comparison catches a promoted file, a leftover staging temp, and a
// same-length in-place rewrite alike.
func treeSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			paths = append(paths, relative+" <dir>")
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // G304: path from the test's own temp tree
		if readErr != nil {
			return readErr
		}
		paths = append(paths, relative+" "+hex.EncodeToString(sha256Sum(data)))
		return nil
	}))
	sort.Strings(paths)
	return paths
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}
