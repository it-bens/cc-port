package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

type codexSessionMeta struct {
	SessionID        string `json:"session_id"`
	ID               string `json:"id"`
	Timestamp        string `json:"timestamp"`
	CWD              string `json:"cwd"`
	FreeText         string `json:"free_text"`
	Originator       string `json:"originator"`
	CLIVersion       string `json:"cli_version"`
	Source           string `json:"source"`
	ModelProvider    string `json:"model_provider"`
	BaseInstructions any    `json:"base_instructions"`
}

type codexTurnContext struct {
	TurnID         string             `json:"turn_id"`
	CWD            string             `json:"cwd"`
	WorkspaceRoots []string           `json:"workspace_roots"`
	ApprovalPolicy string             `json:"approval_policy"`
	SandboxPolicy  codexSandboxPolicy `json:"sandbox_policy"`
}

type codexSandboxPolicy struct {
	Mode string `json:"mode"`
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
		if err := buildStateDatabase(filepath.Join(codexPath, "state_5.sqlite"), projectPath); err != nil {
			return fmt.Errorf("build Codex state database: %w", err)
		}
	}
	if err := buildMemoriesDatabase(filepath.Join(codexPath, "memories_1.sqlite"), projectPath); err != nil {
		return fmt.Errorf("build Codex memories database: %w", err)
	}
	if err := seedCodexMemories(codexPath, projectPath); err != nil {
		return fmt.Errorf("seed Codex memories: %w", err)
	}
	return nil
}

func codexRollout(projectPath string) []byte {
	const rolloutTimestamp = "2026-07-17T10:00:00Z"
	records := []codexRolloutRecord{
		{
			Timestamp: rolloutTimestamp,
			Type:      "session_meta",
			Payload: codexSessionMeta{
				SessionID:        codexThreadID,
				ID:               codexThreadID,
				Timestamp:        rolloutTimestamp,
				CWD:              projectPath,
				FreeText:         "keep " + projectPath + " verbatim unless deep",
				Originator:       "codex_cli_rs",
				CLIVersion:       "0.144.5-fixture",
				Source:           "cli",
				ModelProvider:    "openai",
				BaseInstructions: nil,
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
					Mode: "workspace-write",
				},
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
