package claude_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool/claude"
)

func TestHistoryEntry_RoundTrip(t *testing.T) {
	input := `{"display":"test prompt","timestamp":1234567890,"project":"/Users/test/myproject"}`

	var entry claude.HistoryEntry
	err := json.Unmarshal([]byte(input), &entry)
	require.NoError(t, err)

	assert.Equal(t, "/Users/test/myproject", entry.Project)

	entry.Project = "/Users/test/newproject"
	out, err := json.Marshal(entry)
	require.NoError(t, err)

	var roundTripped map[string]any
	require.NoError(t, json.Unmarshal(out, &roundTripped))
	assert.Equal(t, "test prompt", roundTripped["display"])
	assert.InDelta(t, float64(1234567890), roundTripped["timestamp"], 0)
	assert.Equal(t, "/Users/test/newproject", roundTripped["project"])
}

func TestSessionFile_RoundTrip(t *testing.T) {
	input := `{"pid":12345,"sessionId":"abc","cwd":"/Users/test/myproject","startedAt":999,"kind":"interactive"}`

	var sessionFile claude.SessionFile
	err := json.Unmarshal([]byte(input), &sessionFile)
	require.NoError(t, err)

	assert.Equal(t, "/Users/test/myproject", sessionFile.Cwd)

	sessionFile.Cwd = "/Users/test/newproject"
	out, err := json.Marshal(sessionFile)
	require.NoError(t, err)

	var roundTripped map[string]any
	require.NoError(t, json.Unmarshal(out, &roundTripped))
	assert.InDelta(t, float64(12345), roundTripped["pid"], 0)
	assert.Equal(t, "abc", roundTripped["sessionId"])
	assert.Equal(t, "/Users/test/newproject", roundTripped["cwd"])
}

func TestUserConfig_RoundTrip(t *testing.T) {
	input := `{"numStartups":100,"theme":"dark",` +
		`"projects":{"/Users/test/proj":{"allowedTools":[],"hasTrustDialogAccepted":true}}}`

	var userConfig claude.UserConfig
	err := json.Unmarshal([]byte(input), &userConfig)
	require.NoError(t, err)

	require.Contains(t, userConfig.Projects, "/Users/test/proj")

	userConfig.Projects["/Users/test/newproj"] = userConfig.Projects["/Users/test/proj"]
	delete(userConfig.Projects, "/Users/test/proj")

	out, err := json.Marshal(userConfig)
	require.NoError(t, err)

	var roundTripped map[string]any
	require.NoError(t, json.Unmarshal(out, &roundTripped))
	assert.InDelta(t, float64(100), roundTripped["numStartups"], 0)
	assert.Equal(t, "dark", roundTripped["theme"])

	projects := roundTripped["projects"].(map[string]any)
	assert.Contains(t, projects, "/Users/test/newproj")
	assert.NotContains(t, projects, "/Users/test/proj")
}

func TestUserConfig_RoundTripsUserScopeMCPServers(t *testing.T) {
	input := `{"mcpServers":{"docs":{"type":"http","url":"https://mcp.example.invalid/docs"}},"projects":{}}`

	var userConfig claude.UserConfig
	require.NoError(t, json.Unmarshal([]byte(input), &userConfig))
	require.Contains(t, userConfig.MCPServers, "docs")

	out, err := json.Marshal(userConfig)
	require.NoError(t, err)

	var roundTripped map[string]any
	require.NoError(t, json.Unmarshal(out, &roundTripped))
	servers := roundTripped["mcpServers"].(map[string]any)
	docs := servers["docs"].(map[string]any)
	assert.Equal(t, "http", docs["type"], "keys cc-port does not model must survive the round trip")
	assert.Equal(t, "https://mcp.example.invalid/docs", docs["url"])
}

// A config that never declared user-scope servers must not gain the key, so
// the field is written only when the source carried one.
func TestUserConfig_OmitsMCPServersWhenAbsent(t *testing.T) {
	var userConfig claude.UserConfig
	require.NoError(t, json.Unmarshal([]byte(`{"theme":"dark","projects":{}}`), &userConfig))

	out, err := json.Marshal(userConfig)
	require.NoError(t, err)

	var roundTripped map[string]any
	require.NoError(t, json.Unmarshal(out, &roundTripped))
	assert.NotContains(t, roundTripped, "mcpServers")
}
