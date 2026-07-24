package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool"
)

func TestMCPServersReportsBothTransports(t *testing.T) {
	workspace, _ := fixtureWorkspace(t)

	servers, err := workspace.MCPServers()
	require.NoError(t, err)

	assert.Equal(t, []tool.MCPServer{
		{Name: "filesystem", Command: "node", Args: []string{"--enable-source-maps", "/opt/mcp/filesystem-server.js"}},
		{Name: "search", URL: "https://mcp.example.invalid/search"},
	}, servers)
}

func TestMCPServersReportsNoneWithoutDeclarations(t *testing.T) {
	cases := map[string]string{
		"config without an mcp_servers table": "model = \"gpt-5-fixture\"\n",
		"config with an empty mcp_servers":    "[mcp_servers]\n",
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			workspace := workspaceWithConfig(t, config)

			servers, err := workspace.MCPServers()
			require.NoError(t, err)
			assert.Empty(t, servers)
		})
	}

	t.Run("no config.toml at all", func(t *testing.T) {
		dir := t.TempDir()
		workspace := NewWorkspace(&Home{Dir: dir, SQLiteDir: dir}, fakeGetenv(nil), noProcesses)

		servers, err := workspace.MCPServers()
		require.NoError(t, err)
		assert.Empty(t, servers)
	})
}

// A config.toml that exists but cannot be parsed must fail rather than report
// an empty set: an import plan would otherwise present every archived
// definition as new to a destination whose real declarations it never read.
func TestMCPServersRefusesUnparseableConfig(t *testing.T) {
	workspace := workspaceWithConfig(t, "[mcp_servers.broken\n")

	_, err := workspace.MCPServers()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mcp_servers")
}

// Codex refuses a table naming both launch keys outright, so no transport
// report is faithful. cc-port reports the command, the key Codex tries first,
// rather than failing a whole plan over one malformed table.
func TestMCPServersResolvesConflictingTransportToStdio(t *testing.T) {
	workspace := workspaceWithConfig(t, "[mcp_servers.ambiguous]\ncommand = \"node\"\nurl = \"https://mcp.example.invalid/search\"\n")

	servers, err := workspace.MCPServers()
	require.NoError(t, err)
	assert.Equal(t, []tool.MCPServer{{Name: "ambiguous", Command: "node"}}, servers)
}

// A profile overlay is not read: its definitions report as new rather than
// silently suppressing a definition the active configuration would launch.
func TestMCPServersIgnoresProfileOverlay(t *testing.T) {
	workspace := workspaceWithConfig(t, "model = \"gpt-5-fixture\"\n")
	overlay := filepath.Join(workspace.home.Dir, "work"+configProfileSuffix)
	require.NoError(t, os.WriteFile(overlay, []byte("[mcp_servers.overlay-only]\ncommand = \"node\"\n"), 0o600))

	servers, err := workspace.MCPServers()
	require.NoError(t, err)
	assert.Empty(t, servers)
}

func workspaceWithConfig(t *testing.T, config string) *Workspace {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, configTOMLFileName), []byte(config), 0o600))
	return NewWorkspace(&Home{Dir: dir, SQLiteDir: dir}, fakeGetenv(nil), noProcesses)
}
