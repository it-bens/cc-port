package codex

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/pelletier/go-toml/v2"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

// configProfileSuffix names a per-profile config overlay, which can carry
// its own [projects] table (core/src/config/mod.rs:273, 1757-1763).
const configProfileSuffix = ".config.toml"

// discoverConfigTOMLFiles returns config.toml (if present) followed by
// every <profile>.config.toml file, in sorted order.
func discoverConfigTOMLFiles(home *Home) ([]string, error) {
	var files []string
	configPath := filepath.Join(home.Dir, configTOMLFileName)
	if _, err := os.Stat(configPath); err == nil {
		files = append(files, configPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("stat %s: %w", configPath, err)
	}

	pattern := "*" + configProfileSuffix
	matches, err := filepath.Glob(filepath.Join(home.Dir, pattern))
	if err != nil {
		return nil, fmt.Errorf("glob %s in %s: %w", pattern, home.Dir, err)
	}
	sort.Strings(matches)
	files = append(files, matches...)
	return files, nil
}

// planConfigTOMLFile reports how many key/value occurrences a move would
// rewrite in path. A missing file contributes zero.
func planConfigTOMLFile(path, oldPath, newPath string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path from adapter-controlled config discovery
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	_, count, err := rewrite.TOMLPathRewrite(data, oldPath, newPath)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return count, nil
}

// configTOMLProjectKeys parses path's [projects] table and returns its
// top-level keys. A missing file contributes no keys. This parses the TOML
// structure rather than scanning raw bytes, so a path occurring only in a
// comment or an unrelated value never surfaces as a key.
func configTOMLProjectKeys(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path from adapter-controlled config discovery
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var config struct {
		Projects map[string]any `toml:"projects"`
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse %s projects: %w", path, err)
	}
	keys := make([]string, 0, len(config.Projects))
	for key := range config.Projects {
		keys = append(keys, key)
	}
	return keys, nil
}

// MCPServers implements tool.Importer: the MCP server definitions
// config.toml declares on this machine, sorted by name. A missing config.toml
// declares none; one that exists but cannot be read or parsed is an error,
// never an empty set.
//
// Only config.toml itself is read, not the <profile>.config.toml overlays
// discoverConfigTOMLFiles walks. A definition an unread overlay already
// declares merely reports as new; the opposite error would hide a definition
// that does launch here.
func (workspace *Workspace) MCPServers() ([]tool.MCPServer, error) {
	return configTOMLMCPServers(filepath.Join(workspace.home.Dir, configTOMLFileName))
}

// ArchiveMCPServers implements tool.Importer: nil for every entry, meaning
// none is recognized. Codex's MCP server definitions live only in
// config.toml, which is never exported and never imported, so no archive
// entry can carry one and no plan runs Codex's destination read on an
// archive's account.
func (workspace *Workspace) ArchiveMCPServers(archive.Entry, map[string]string) ([]tool.MCPServer, error) {
	return nil, nil
}

// configTOMLMCPServers parses path's [mcp_servers] table into contract values
// sorted by name. A missing file contributes none. A command wins over a url,
// following the order Codex tries the two in (config/src/mcp_types.rs, TryFrom
// for RawMcpServerConfig). Codex refuses a table naming both outright, so no
// transport report is faithful for one; reporting the command at least names
// an executable the table asked for, and refusing the whole plan over one
// malformed table would be a worse answer.
func configTOMLMCPServers(path string) ([]tool.MCPServer, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path from adapter-controlled config discovery
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var config struct {
		MCPServers map[string]struct {
			Command string   `toml:"command"`
			Args    []string `toml:"args"`
			URL     string   `toml:"url"`
		} `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse %s mcp_servers: %w", path, err)
	}
	if len(config.MCPServers) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(config.MCPServers))
	for name := range config.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	servers := make([]tool.MCPServer, 0, len(names))
	for _, name := range names {
		definition := config.MCPServers[name]
		server := tool.MCPServer{Name: name, URL: definition.URL}
		if definition.Command != "" {
			server = tool.MCPServer{Name: name, Command: definition.Command, Args: definition.Args}
		}
		servers = append(servers, server)
	}
	return servers, nil
}

// configTOMLKnowsProject reports whether any config.toml/profile file has a
// [projects] key matching project, using the same equality-or-/-boundary
// predicate pathMatchesProject applies to thread and rollout cwds. This
// holds even when Codex has recorded no thread rows or rollouts for project
// yet (a trust entry created before the first session), so callers use it
// as a third, independent association alongside state-database and rollout
// evidence.
func configTOMLKnowsProject(home *Home, project string) (bool, error) {
	matches, err := configTOMLProjectMatches(home, project)
	return len(matches) > 0, err
}

// configTOMLProjectMatches returns the literal [projects] keys which
// canonically match project, grouped by config file.  The stored key, rather
// than project, is the only safe source for TOML's literal rewrite primitive.
func configTOMLProjectMatches(home *Home, project string) (map[string][]string, error) {
	files, err := discoverConfigTOMLFiles(home)
	if err != nil {
		return nil, err
	}
	matches := make(map[string][]string)
	for _, path := range files {
		keys, err := configTOMLProjectKeys(path)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			matched, err := pathMatchesProject(key, project)
			if err != nil {
				return nil, err
			}
			if matched {
				matches[path] = append(matches[path], key)
			}
		}
	}
	return matches, nil
}

// applyConfigTOMLFile rewrites path in place via rewrite.TOMLPathRewrite,
// wrapping the primitive's validation errors (which report bytes only)
// with the file path.
func applyConfigTOMLFile(path, oldPath, newPath string, undo *tool.Restorer) (int, error) {
	return applyConfigTOMLKeys(path, []string{oldPath}, newPath, undo)
}

func applyConfigTOMLKeys(path string, keys []string, newPath string, undo *tool.Restorer) (int, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := undo.RegisterFile(path); err != nil {
		return 0, fmt.Errorf("back up %s: %w", path, err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path from adapter-controlled config discovery
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	rewritten := data
	total := 0
	for _, key := range keys {
		var count int
		rewritten, count, err = rewrite.TOMLPathRewrite(rewritten, key, newPath)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", path, err)
		}
		if count == 0 {
			return 0, fmt.Errorf("rewrite matched config trust key %q in %s: zero keys rewritten", key, path)
		}
		total += count
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := rewrite.SafeWriteFile(path, rewritten, info.Mode()); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	return total, nil
}
