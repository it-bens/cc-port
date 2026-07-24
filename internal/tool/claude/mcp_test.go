package claude_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func TestMCPServersReportsUserScopeDefinitions(t *testing.T) {
	workspace := claude.NewWorkspace(testutil.SetupFixture(t))

	servers, err := workspace.MCPServers()
	require.NoError(t, err)

	assert.Equal(t, []tool.MCPServer{
		{Name: "docs", URL: "https://mcp.example.invalid/docs"},
		{Name: "playwright", Command: "npx", Args: []string{"-y", "@playwright/mcp@latest"}},
	}, servers)
}

func TestMCPServersReportsNoneWithoutDeclarations(t *testing.T) {
	cases := map[string]string{
		"config without an mcpServers key": `{"projects":{"/Users/test/Projects/myproject":{}}}`,
		"config with an empty mcpServers":  `{"mcpServers":{},"projects":{}}`,
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			workspace := claude.NewWorkspace(homeWithConfig(t, config))

			servers, err := workspace.MCPServers()
			require.NoError(t, err)
			assert.Empty(t, servers)
		})
	}

	t.Run("no config file at all", func(t *testing.T) {
		dir := t.TempDir()
		workspace := claude.NewWorkspace(&claude.Home{
			Dir:        filepath.Join(dir, ".claude"),
			ConfigFile: filepath.Join(dir, ".claude.json"),
		})

		servers, err := workspace.MCPServers()
		require.NoError(t, err)
		assert.Empty(t, servers)
	})
}

// A command settles the transport on its own, so reporting the url alongside
// it would describe a server Claude Code never opens.
func TestMCPServersResolvesConflictingTransportToStdio(t *testing.T) {
	workspace := claude.NewWorkspace(homeWithConfig(t,
		`{"mcpServers":{"ambiguous":{"command":"npx","url":"https://mcp.example.invalid/docs"}}}`))

	servers, err := workspace.MCPServers()
	require.NoError(t, err)
	assert.Equal(t, []tool.MCPServer{{Name: "ambiguous", Command: "npx"}}, servers)
}

// A config file that exists but cannot be parsed must fail rather than report
// an empty set: an import plan would otherwise present every archived
// definition as new to a destination whose real declarations it never read.
// The error is the same InvalidConfigJSONError the apply's finalize splice
// raises, so a plan surfaces the failure an apply would hit after promotion.
func TestMCPServersRefusesUnparseableConfig(t *testing.T) {
	workspace := claude.NewWorkspace(homeWithConfig(t, `{"mcpServers":`))

	_, err := workspace.MCPServers()
	var invalidConfig *claude.InvalidConfigJSONError
	require.ErrorAs(t, err, &invalidConfig)
}

func homeWithConfig(t *testing.T, config string) *claude.Home {
	t.Helper()
	dir := t.TempDir()
	configFile := filepath.Join(dir, ".claude.json")
	require.NoError(t, os.WriteFile(configFile, []byte(config), 0o600))
	return &claude.Home{Dir: filepath.Join(dir, ".claude"), ConfigFile: configFile}
}
