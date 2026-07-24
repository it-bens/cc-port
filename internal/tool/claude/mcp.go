package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/tool"
)

// mcpServerDefinition is the launch-relevant subset of one ~/.claude.json
// mcpServers entry. Claude Code starts a stdio server by running Command with
// Args and reaches an HTTP- or SSE-transport server at URL; every other key
// (env, headers, transport type) is irrelevant to what cc-port reports.
type mcpServerDefinition struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	URL     string   `json:"url"`
}

// MCPServers implements tool.Importer: the user-scope MCP server definitions
// ~/.claude.json declares on this machine, sorted by name. A config file that
// does not exist declares none; one that exists but cannot be read or parsed
// is an error, never an empty set.
func (workspace *Workspace) MCPServers() ([]tool.MCPServer, error) {
	data, err := os.ReadFile(workspace.home.ConfigFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config file %q: %w", workspace.home.ConfigFile, err)
	}
	var userConfig UserConfig
	if err := json.Unmarshal(data, &userConfig); err != nil {
		return nil, fmt.Errorf("unmarshal config file %q: %w", workspace.home.ConfigFile, err)
	}
	return decodeMCPServers(userConfig.MCPServers, workspace.home.ConfigFile)
}

// ArchiveMCPServers implements tool.Importer: the definitions carried by the
// project block Finalize splices into ~/.claude.json. The block's mcpServers
// are project-scope but launch with any session opened on the project, so the
// plan reports them exactly as it reports user-scope ones.
func (workspace *Workspace) ArchiveMCPServers(
	entry archive.Entry, resolutions map[string]string,
) ([]tool.MCPServer, error) {
	if entry.Name != configEntryName {
		return nil, nil
	}
	resolved, err := resolveConfigEntryBytes(entry.Name, entry, resolutions)
	if err != nil {
		return nil, err
	}
	var block struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(resolved, &block); err != nil {
		return nil, fmt.Errorf("unmarshal archive entry %q: %w", entry.Name, err)
	}
	return decodeMCPServers(block.MCPServers, "archive entry "+entry.Name)
}

// decodeMCPServers turns a raw mcpServers map into contract values sorted by
// name. source names the file or archive entry the map came from, so a
// malformed definition reports where it lives.
func decodeMCPServers(raw map[string]json.RawMessage, source string) ([]tool.MCPServer, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)

	servers := make([]tool.MCPServer, 0, len(names))
	for _, name := range names {
		var definition mcpServerDefinition
		if err := json.Unmarshal(raw[name], &definition); err != nil {
			return nil, fmt.Errorf("unmarshal mcpServers entry %q in %s: %w", name, source, err)
		}
		server := tool.MCPServer{Name: name, URL: definition.URL}
		// A command names the executable Claude Code spawns, which settles the
		// transport on its own; reporting a stray url alongside it would
		// describe a launch that never happens.
		if definition.Command != "" {
			server = tool.MCPServer{Name: name, Command: definition.Command, Args: definition.Args}
		}
		servers = append(servers, server)
	}
	return servers, nil
}
