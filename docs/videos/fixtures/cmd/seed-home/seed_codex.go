package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/it-bens/cc-port/internal/tool/codex/codexschema"
)

const (
	codexThreadID   = "00000000-0000-4000-8000-000000000001"
	rolloutRelative = "sessions/2026/07/17/rollout-2026-07-17T10-00-00-00000000-0000-4000-8000-000000000001.jsonl"
)

// codexTargetConfig is the teammate machine's keyless Codex config for the
// paired demo clips. The model turn (the demo's reindex trigger) routes
// through a custom OpenAI provider with retries disabled so it fails fast on
// one 401 instead of a ~20s reconnect storm; the built-in openai provider
// cannot be overridden and keeps its five-retry default. Pinning no model
// lets Codex use its bundled default, which carries metadata and so avoids
// the "model metadata not found" warning a synthetic name would trigger.
const codexTargetConfig = `# Synthetic Codex fixture configuration.
model_provider = "openai-custom"

[model_providers.openai-custom]
name = "OpenAI"
base_url = "https://api.openai.com/v1"
wire_api = "responses"
requires_openai_auth = true
request_max_retries = 0
stream_max_retries = 0
`

type codexHistoryRecord struct {
	SessionID string `json:"session_id"`
	Timestamp int64  `json:"ts"`
	Text      string `json:"text"`
}

type codexSessionIndexRecord struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
	UpdatedAt  int64  `json:"updated_at"`
}

type codexRolloutRecord struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   any    `json:"payload"`
}

// The rollout record shapes below mirror what Codex writes today
// (protocol/src/protocol.rs SessionMeta, TurnContextItem, ThreadSettings) and
// the test fixture at internal/tool/codex/testdata/dotcodex/sessions, so the
// demo exercises every path field the move surfaces rewrite.
type codexSessionMeta struct {
	SessionID             string   `json:"session_id"`
	ID                    string   `json:"id"`
	Timestamp             string   `json:"timestamp"`
	CWD                   string   `json:"cwd"`
	RuntimeWorkspaceRoots []string `json:"runtime_workspace_roots"`
	FreeText              string   `json:"free_text"`
	Originator            string   `json:"originator"`
	CLIVersion            string   `json:"cli_version"`
	Source                string   `json:"source"`
	ModelProvider         string   `json:"model_provider"`
	BaseInstructions      any      `json:"base_instructions"`
}

type codexThreadSettingsApplied struct {
	Type           string              `json:"type"`
	ThreadID       string              `json:"thread_id"`
	ThreadSettings codexThreadSettings `json:"thread_settings"`
}

type codexThreadSettings struct {
	Model                 string                 `json:"model"`
	ModelProviderID       string                 `json:"model_provider_id"`
	ApprovalPolicy        string                 `json:"approval_policy"`
	ApprovalsReviewer     string                 `json:"approvals_reviewer"`
	PermissionProfile     codexPermissionProfile `json:"permission_profile"`
	CWD                   string                 `json:"cwd"`
	RuntimeWorkspaceRoots []string               `json:"runtime_workspace_roots"`
	CollaborationMode     codexCollaborationMode `json:"collaboration_mode"`
	DisabledPluginIDs     []string               `json:"disabled_plugin_ids"`
}

type codexCollaborationMode struct {
	Mode     string                         `json:"mode"`
	Settings codexCollaborationModeSettings `json:"settings"`
}

type codexCollaborationModeSettings struct {
	Model                 string `json:"model"`
	ReasoningEffort       any    `json:"reasoning_effort"`
	DeveloperInstructions any    `json:"developer_instructions"`
}

type codexTurnContext struct {
	TurnID                  string                 `json:"turn_id"`
	CWD                     string                 `json:"cwd"`
	WorkspaceRoots          []string               `json:"workspace_roots"`
	ApprovalPolicy          string                 `json:"approval_policy"`
	SandboxPolicy           codexSandboxPolicy     `json:"sandbox_policy"`
	PermissionProfile       codexPermissionProfile `json:"permission_profile"`
	FileSystemSandboxPolicy codexFileSystemSandbox `json:"file_system_sandbox_policy"`
	Model                   string                 `json:"model"`
	Summary                 string                 `json:"summary"`
}

type codexSandboxPolicy struct {
	Type                string   `json:"type"`
	WritableRoots       []string `json:"writable_roots"`
	NetworkAccess       bool     `json:"network_access"`
	ExcludeTmpdirEnvVar bool     `json:"exclude_tmpdir_env_var"`
	ExcludeSlashTmp     bool     `json:"exclude_slash_tmp"`
}

type codexPermissionProfile struct {
	Type       string               `json:"type"`
	FileSystem codexFileSystemRules `json:"file_system"`
	Network    string               `json:"network"`
}

type codexFileSystemRules struct {
	Type    string                 `json:"type"`
	Entries []codexFileSystemEntry `json:"entries"`
}

type codexFileSystemSandbox struct {
	Kind    string                 `json:"kind"`
	Entries []codexFileSystemEntry `json:"entries"`
}

type codexFileSystemEntry struct {
	Path   codexFileSystemPath `json:"path"`
	Access string              `json:"access"`
}

// codexFileSystemPath is the tagged path union: `special` entries carry a
// symbolic `value`, `path` entries an absolute `path`.
type codexFileSystemPath struct {
	Type  string `json:"type"`
	Value any    `json:"value,omitempty"`
	Path  string `json:"path,omitempty"`
}

func codexProjectEntries(projectPath string) []codexFileSystemEntry {
	return []codexFileSystemEntry{
		{Path: codexFileSystemPath{Type: "special", Value: map[string]string{"kind": "root"}}, Access: "read"},
		{Path: codexFileSystemPath{Type: "path", Path: projectPath}, Access: "write"},
	}
}

type codexResponseItem struct {
	Type    string                 `json:"type"`
	Role    string                 `json:"role"`
	Content []codexResponseContent `json:"content"`
}

type codexResponseContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func seedCodex(homePath, projectPath, role string, codexStateDB bool) error {
	codexPath := filepath.Join(homePath, ".codex")
	if err := os.MkdirAll(codexPath, 0o700); err != nil {
		return fmt.Errorf("create Codex directory: %w", err)
	}
	if role == roleTarget {
		if err := os.MkdirAll(filepath.Join(codexPath, "sessions"), 0o700); err != nil {
			return fmt.Errorf("create empty Codex sessions directory: %w", err)
		}
		if err := os.MkdirAll(filepath.Join(codexPath, "archived_sessions"), 0o700); err != nil {
			return fmt.Errorf("create empty Codex archived sessions directory: %w", err)
		}
		if err := writeFixtureFile(filepath.Join(codexPath, "config.toml"), []byte(codexTargetConfig)); err != nil {
			return fmt.Errorf("write Codex target config: %w", err)
		}
		return nil
	}

	config := "# Synthetic Codex fixture configuration.\n" +
		"model = \"gpt-5-fixture\"\n\n" +
		"[projects." + strconv.Quote(projectPath) + "]\n" +
		"trust_level = \"trusted\"\n"
	if err := writeFixtureFile(filepath.Join(codexPath, "config.toml"), []byte(config)); err != nil {
		return fmt.Errorf("write Codex source config: %w", err)
	}
	rolloutPath := filepath.Join(codexPath, filepath.FromSlash(rolloutRelative))
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o700); err != nil {
		return fmt.Errorf("create Codex rollout directory: %w", err)
	}
	rollout := codexRollout(projectPath)
	if err := writeFixtureFile(rolloutPath, rollout); err != nil {
		return fmt.Errorf("write Codex rollout: %w", err)
	}
	historyRecord, err := json.Marshal(codexHistoryRecord{
		SessionID: codexThreadID,
		Timestamp: 1784282400,
		Text:      "Worked in " + projectPath,
	})
	if err != nil {
		return fmt.Errorf("marshal Codex history record: %w", err)
	}
	if err := writeFixtureFile(filepath.Join(codexPath, "history.jsonl"), append(historyRecord, '\n')); err != nil {
		return fmt.Errorf("write Codex history: %w", err)
	}
	indexRecord, err := json.Marshal(codexSessionIndexRecord{
		ID:         codexThreadID,
		ThreadName: "fix login bug",
		UpdatedAt:  1784282400,
	})
	if err != nil {
		return fmt.Errorf("marshal Codex session index record: %w", err)
	}
	if err := writeFixtureFile(filepath.Join(codexPath, "session_index.jsonl"), append(indexRecord, '\n')); err != nil {
		return fmt.Errorf("write Codex session index: %w", err)
	}
	// The state database is Codex's rebuildable cache: a paired export/import
	// demo omits it so the teammate rebuilds the thread index from the imported
	// rollout, and the empty threads sidecar leaves nothing to warn about.
	if codexStateDB {
		if err := buildStateDatabase(filepath.Join(codexPath, codexschema.StateDBFileName), projectPath); err != nil {
			return fmt.Errorf("build Codex state database: %w", err)
		}
	}
	if err := buildMemoriesDatabase(filepath.Join(codexPath, codexschema.MemoriesDBFileName), projectPath); err != nil {
		return fmt.Errorf("build Codex memories database: %w", err)
	}
	if err := seedCodexMemories(codexPath, projectPath); err != nil {
		return fmt.Errorf("seed Codex memories: %w", err)
	}
	return nil
}

func codexRollout(projectPath string) []byte {
	const rolloutTimestamp = "2026-07-17T10:00:00Z"
	const model = "gpt-5-fixture"
	const restricted = "restricted"
	profile := codexPermissionProfile{
		Type:       "managed",
		FileSystem: codexFileSystemRules{Type: restricted, Entries: codexProjectEntries(projectPath)},
		Network:    restricted,
	}
	records := []codexRolloutRecord{
		{
			Timestamp: rolloutTimestamp,
			Type:      "session_meta",
			Payload: codexSessionMeta{
				SessionID:             codexThreadID,
				ID:                    codexThreadID,
				Timestamp:             rolloutTimestamp,
				CWD:                   projectPath,
				RuntimeWorkspaceRoots: []string{projectPath},
				FreeText:              "keep " + projectPath + " verbatim unless deep",
				Originator:            "codex_cli_rs",
				CLIVersion:            "0.144.5-fixture",
				Source:                "cli",
				ModelProvider:         "openai",
				BaseInstructions:      nil,
			},
		},
		{
			Timestamp: rolloutTimestamp,
			Type:      "event_msg",
			Payload: codexThreadSettingsApplied{
				Type:     "thread_settings_applied",
				ThreadID: codexThreadID,
				ThreadSettings: codexThreadSettings{
					Model:                 model,
					ModelProviderID:       "openai",
					ApprovalPolicy:        "on-request",
					ApprovalsReviewer:     userValue,
					PermissionProfile:     profile,
					CWD:                   projectPath,
					RuntimeWorkspaceRoots: []string{projectPath},
					CollaborationMode:     codexCollaborationMode{Mode: "default", Settings: codexCollaborationModeSettings{Model: model}},
					DisabledPluginIDs:     []string{},
				},
			},
		},
		{
			Timestamp: rolloutTimestamp,
			Type:      "turn_context",
			Payload: codexTurnContext{
				TurnID:         "turn-1",
				CWD:            projectPath,
				WorkspaceRoots: []string{projectPath},
				ApprovalPolicy: "on-request",
				SandboxPolicy: codexSandboxPolicy{
					Type:          "workspace-write",
					WritableRoots: []string{projectPath + "/build"},
				},
				PermissionProfile:       profile,
				FileSystemSandboxPolicy: codexFileSystemSandbox{Kind: restricted, Entries: codexProjectEntries(projectPath)},
				Model:                   model,
				Summary:                 "auto",
			},
		},
		{
			Timestamp: rolloutTimestamp,
			Type:      "response_item",
			Payload: codexResponseItem{
				Type: "message",
				Role: assistantValue,
				Content: []codexResponseContent{{
					Type: "output_text",
					Text: "I edited " + projectPath + "/src/main.py to fix the bug.",
				}},
			},
		},
	}

	rollout := make([]byte, 0)
	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			panic(fmt.Sprintf("marshal static Codex rollout record: %v", err))
		}
		rollout = append(rollout, data...)
		rollout = append(rollout, '\n')
	}
	return rollout
}

func seedCodexMemories(codexPath, projectPath string) error {
	memoriesPath := filepath.Join(codexPath, "memories")
	if err := os.MkdirAll(filepath.Join(memoriesPath, "rollout_summaries"), 0o700); err != nil {
		return fmt.Errorf("create Codex rollout summaries directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(memoriesPath, ".git"), 0o700); err != nil {
		return fmt.Errorf("create Codex memories git directory: %w", err)
	}
	rawMemory := "# Raw Memories\n\n## Thread `" + codexThreadID + "`\ncwd: " + projectPath + "\n\nFixed a bug in " + projectPath + "/src/main.py.\n"
	if err := writeFixtureFile(filepath.Join(memoriesPath, "raw_memories.md"), []byte(rawMemory)); err != nil {
		return fmt.Errorf("write Codex raw memories: %w", err)
	}
	summary := "cwd: " + projectPath + "\n\nSummary: fixed a bug in " + projectPath + "/src/main.py.\n"
	if err := writeFixtureFile(filepath.Join(memoriesPath, "rollout_summaries", "2026-07-17T10-00-00-a1b2.md"), []byte(summary)); err != nil {
		return fmt.Errorf("write Codex rollout summary: %w", err)
	}
	if err := writeFixtureFile(filepath.Join(memoriesPath, ".git", "config"), []byte("[core]\n\trepositoryformatversion = 0\n")); err != nil {
		return fmt.Errorf("write Codex memories git config: %w", err)
	}
	return nil
}
