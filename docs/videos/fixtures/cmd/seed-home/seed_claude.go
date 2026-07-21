package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/it-bens/cc-port/internal/tool/claude"
)

const (
	claudeSessionID = "00000000-0000-0000-0000-000000000001"
	assistantValue  = "assistant"
)

type claudeTranscriptRecord struct {
	Type      string                  `json:"type"`
	CWD       string                  `json:"cwd"`
	SessionID string                  `json:"sessionId"`
	Message   claudeTranscriptMessage `json:"message"`
	Timestamp string                  `json:"timestamp"`
}

type claudeTranscriptMessage struct {
	Role    string                    `json:"role"`
	Content []claudeTranscriptContent `json:"content"`
}

type claudeTranscriptContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeHistoryRecord struct {
	Project        string         `json:"project"`
	Display        string         `json:"display"`
	PastedContents map[string]any `json:"pastedContents"`
}

func seedClaude(homePath, projectPath, role string) error {
	claudePath := filepath.Join(homePath, ".claude")
	if err := os.MkdirAll(filepath.Join(claudePath, "projects"), 0o700); err != nil {
		return fmt.Errorf("create Claude projects directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(claudePath, "file-history"), 0o700); err != nil {
		return fmt.Errorf("create Claude file history directory: %w", err)
	}

	if role == roleTarget {
		if err := writeFixtureFile(filepath.Join(claudePath, "history.jsonl"), nil); err != nil {
			return fmt.Errorf("write empty Claude history: %w", err)
		}
		if err := writeJSONFixture(filepath.Join(homePath, ".claude.json"), map[string]any{"numStartups": 0, "projects": map[string]any{}}); err != nil {
			return fmt.Errorf("write Claude target config: %w", err)
		}
		return nil
	}

	encodedProjectPath := claude.EncodePath(projectPath)
	projectDirectory := filepath.Join(claudePath, "projects", encodedProjectPath)
	if err := os.MkdirAll(projectDirectory, 0o700); err != nil {
		return fmt.Errorf("create Claude project directory: %w", err)
	}

	userRecord, err := json.Marshal(claudeTranscriptRecord{
		Type:      "user",
		CWD:       projectPath,
		SessionID: claudeSessionID,
		Message: claudeTranscriptMessage{
			Role: "user",
			Content: []claudeTranscriptContent{{
				Type: "text",
				Text: "hello from " + projectPath,
			}},
		},
		Timestamp: "2026-05-01T12:00:00Z",
	})
	if err != nil {
		return fmt.Errorf("marshal Claude user transcript record: %w", err)
	}
	assistantRecord, err := json.Marshal(claudeTranscriptRecord{
		Type:      assistantValue,
		CWD:       projectPath,
		SessionID: claudeSessionID,
		Message: claudeTranscriptMessage{
			Role: assistantValue,
			Content: []claudeTranscriptContent{{
				Type: "text",
				Text: "reply from " + projectPath,
			}},
		},
		Timestamp: "2026-05-01T12:00:01Z",
	})
	if err != nil {
		return fmt.Errorf("marshal Claude assistant transcript record: %w", err)
	}
	transcript := append(append(userRecord, '\n'), assistantRecord...)
	transcript = append(transcript, '\n')
	if err := writeFixtureFile(filepath.Join(projectDirectory, claudeSessionID+".jsonl"), transcript); err != nil {
		return fmt.Errorf("write Claude transcript: %w", err)
	}

	history, err := json.Marshal(claudeHistoryRecord{
		Project:        projectPath,
		Display:        projectPath,
		PastedContents: map[string]any{},
	})
	if err != nil {
		return fmt.Errorf("marshal Claude history: %w", err)
	}
	if err := writeFixtureFile(filepath.Join(claudePath, "history.jsonl"), append(history, '\n')); err != nil {
		return fmt.Errorf("write Claude history: %w", err)
	}
	config := map[string]any{
		"numStartups": 1,
		"projects": map[string]any{
			projectPath: map[string]any{
				"allowedTools":   []any{},
				"history":        []any{},
				"mcpContextUris": []any{},
			},
		},
	}
	if err := writeJSONFixture(filepath.Join(homePath, ".claude.json"), config); err != nil {
		return fmt.Errorf("write Claude source config: %w", err)
	}
	return nil
}

func writeJSONFixture(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON fixture %q: %w", path, err)
	}
	if err := writeFixtureFile(path, append(data, '\n')); err != nil {
		return fmt.Errorf("write JSON fixture %q: %w", path, err)
	}
	return nil
}

func writeFixtureFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write fixture file %q: %w", path, err)
	}
	return nil
}
