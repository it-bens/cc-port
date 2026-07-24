// Package claude provides types and utilities for Claude Code data files.
package claude

import "encoding/json"

// MaxHistoryLine bounds a single history.jsonl line read through
// bufio.Scanner. Claude Code can embed pastedContents inline on one
// line; 16 MiB covers plausible legitimate cases while rejecting
// pathological inputs with bufio.ErrTooLong instead of silent
// truncation.
const MaxHistoryLine = 16 << 20

// HistoryEntry is one line of history.jsonl.
type HistoryEntry struct {
	Project string                     `json:"project"`
	Extra   map[string]json.RawMessage `json:"-"`
}

// SessionFile is the structure of sessions/<pid>.json.
type SessionFile struct {
	Cwd   string                     `json:"cwd"`
	Pid   int                        `json:"pid"`
	Extra map[string]json.RawMessage `json:"-"`
}

// UserConfig is the top-level structure of ~/.claude.json. MCPServers holds
// the user-scope MCP server definitions, keyed by the name Claude Code
// registers each under; a nil map means the file declares none. Each
// definition stays raw so a round trip preserves the keys cc-port does not
// model (env, headers, transport type).
type UserConfig struct {
	Projects   map[string]json.RawMessage `json:"projects"`
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
	Extra      map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON implements json.Unmarshaler for HistoryEntry,
// preserving unknown fields in Extra.
func (historyEntry *HistoryEntry) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &historyEntry.Extra); err != nil {
		return err
	}
	if value, ok := historyEntry.Extra["project"]; ok {
		if err := json.Unmarshal(value, &historyEntry.Project); err != nil {
			return err
		}
		delete(historyEntry.Extra, "project")
	}
	return nil
}

// MarshalJSON implements json.Marshaler for HistoryEntry,
// merging Extra fields back into the output.
func (historyEntry HistoryEntry) MarshalJSON() ([]byte, error) {
	merged := make(map[string]any, len(historyEntry.Extra)+1)
	for key, value := range historyEntry.Extra {
		merged[key] = value
	}
	merged["project"] = historyEntry.Project
	return json.Marshal(merged)
}

// UnmarshalJSON implements json.Unmarshaler for SessionFile,
// preserving unknown fields in Extra.
func (sessionFile *SessionFile) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &sessionFile.Extra); err != nil {
		return err
	}
	if value, ok := sessionFile.Extra["cwd"]; ok {
		if err := json.Unmarshal(value, &sessionFile.Cwd); err != nil {
			return err
		}
		delete(sessionFile.Extra, "cwd")
	}
	if value, ok := sessionFile.Extra["pid"]; ok {
		if err := json.Unmarshal(value, &sessionFile.Pid); err != nil {
			return err
		}
		delete(sessionFile.Extra, "pid")
	}
	return nil
}

// MarshalJSON implements json.Marshaler for SessionFile,
// merging Extra fields back into the output.
func (sessionFile SessionFile) MarshalJSON() ([]byte, error) {
	merged := make(map[string]any, len(sessionFile.Extra)+2)
	for key, value := range sessionFile.Extra {
		merged[key] = value
	}
	merged["cwd"] = sessionFile.Cwd
	merged["pid"] = sessionFile.Pid
	return json.Marshal(merged)
}

// UnmarshalJSON implements json.Unmarshaler for UserConfig,
// preserving unknown fields in Extra.
func (userConfig *UserConfig) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &userConfig.Extra); err != nil {
		return err
	}
	if value, ok := userConfig.Extra["projects"]; ok {
		if err := json.Unmarshal(value, &userConfig.Projects); err != nil {
			return err
		}
		delete(userConfig.Extra, "projects")
	}
	if value, ok := userConfig.Extra["mcpServers"]; ok {
		if err := json.Unmarshal(value, &userConfig.MCPServers); err != nil {
			return err
		}
		delete(userConfig.Extra, "mcpServers")
	}
	return nil
}

// MarshalJSON implements json.Marshaler for UserConfig,
// merging Extra fields back into the output. A nil MCPServers is omitted
// rather than written, normalizing an explicit "mcpServers": null to an
// absent key — the two say the same thing, and writing the key back onto
// the far more common config that never had it would be the worse loss.
func (userConfig UserConfig) MarshalJSON() ([]byte, error) {
	merged := make(map[string]any, len(userConfig.Extra)+2)
	for key, value := range userConfig.Extra {
		merged[key] = value
	}
	merged["projects"] = userConfig.Projects
	if userConfig.MCPServers != nil {
		merged["mcpServers"] = userConfig.MCPServers
	}
	return json.Marshal(merged)
}
