package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

const stateDatabaseSchema = `
CREATE TABLE threads (
	id TEXT PRIMARY KEY,
	rollout_path TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	source TEXT NOT NULL,
	model_provider TEXT NOT NULL,
	cwd TEXT NOT NULL,
	title TEXT NOT NULL,
	sandbox_policy TEXT NOT NULL,
	approval_mode TEXT NOT NULL,
	tokens_used INTEGER NOT NULL DEFAULT 0,
	has_user_event INTEGER NOT NULL DEFAULT 0,
	archived INTEGER NOT NULL DEFAULT 0,
	archived_at INTEGER,
	git_sha TEXT,
	git_branch TEXT,
	git_origin_url TEXT
);
CREATE TABLE backfill_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	status TEXT NOT NULL,
	last_watermark TEXT,
	last_success_at INTEGER,
	updated_at INTEGER NOT NULL
);
CREATE TABLE agent_jobs (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	status TEXT NOT NULL,
	instruction TEXT NOT NULL,
	output_schema_json TEXT,
	input_headers_json TEXT NOT NULL,
	input_csv_path TEXT NOT NULL,
	output_csv_path TEXT NOT NULL,
	auto_export INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	started_at INTEGER,
	completed_at INTEGER,
	last_error TEXT
);`

const memoriesDatabaseSchema = `
CREATE TABLE stage1_outputs (
	thread_id TEXT PRIMARY KEY,
	source_updated_at INTEGER NOT NULL,
	raw_memory TEXT NOT NULL,
	rollout_summary TEXT NOT NULL,
	rollout_slug TEXT,
	generated_at INTEGER NOT NULL,
	usage_count INTEGER,
	last_usage INTEGER,
	selected_for_phase2 INTEGER NOT NULL DEFAULT 0,
	selected_for_phase2_source_updated_at INTEGER
);`

func buildStateDatabase(path, projectPath string) (returnError error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open state database %q: %w", path, err)
	}
	closed := false
	defer func() {
		if !closed {
			if closeError := database.Close(); closeError != nil {
				returnError = errors.Join(
					returnError,
					fmt.Errorf("close state database %q: %w", path, closeError),
				)
			}
		}
	}()
	if err := database.PingContext(context.Background()); err != nil {
		return fmt.Errorf("connect state database %q: %w", path, err)
	}
	if _, err := database.ExecContext(context.Background(), stateDatabaseSchema); err != nil {
		return fmt.Errorf("create state database schema: %w", err)
	}
	now := time.Now().Unix()
	if err := insertStateRows(database, projectPath, now); err != nil {
		return fmt.Errorf("insert state database rows: %w", err)
	}
	if err := database.Close(); err != nil {
		closed = true
		return fmt.Errorf("close state database %q: %w", path, err)
	}
	closed = true
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set state database permissions %q: %w", path, err)
	}
	return nil
}

func insertStateRows(database *sql.DB, projectPath string, now int64) error {
	if _, err := database.ExecContext(context.Background(), `INSERT INTO threads
		(id, rollout_path, created_at, updated_at, source, model_provider, cwd, title, sandbox_policy, approval_mode)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		codexThreadID, rolloutRelative, now, now, "cli", "openai", projectPath, "fix login bug", "workspace-write", "on-request"); err != nil {
		return fmt.Errorf("insert threads row: %w", err)
	}
	const backfillEpoch = 1_752_137_200
	if _, err := database.ExecContext(context.Background(), `INSERT INTO backfill_state (id, status, last_watermark, last_success_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, 1, "complete", rolloutRelative, backfillEpoch, backfillEpoch); err != nil {
		return fmt.Errorf("insert backfill state row: %w", err)
	}
	inputCSVPath := projectPath + "/data/input.csv"
	outputCSVPath := projectPath + "/data/output.csv"
	if _, err := database.ExecContext(context.Background(), `INSERT INTO agent_jobs
		(id, name, status, instruction, input_headers_json, input_csv_path, output_csv_path, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"job-1", "fixture-job", "completed", "process rows", `["col"]`,
		inputCSVPath, outputCSVPath, now, now); err != nil {
		return fmt.Errorf("insert agent jobs row: %w", err)
	}
	return nil
}

func buildMemoriesDatabase(path, projectPath string) (returnError error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open memories database %q: %w", path, err)
	}
	closed := false
	defer func() {
		if !closed {
			if closeError := database.Close(); closeError != nil {
				returnError = errors.Join(
					returnError,
					fmt.Errorf("close memories database %q: %w", path, closeError),
				)
			}
		}
	}()
	if err := database.PingContext(context.Background()); err != nil {
		return fmt.Errorf("connect memories database %q: %w", path, err)
	}
	if _, err := database.ExecContext(context.Background(), memoriesDatabaseSchema); err != nil {
		return fmt.Errorf("create memories database schema: %w", err)
	}
	now := time.Now().Unix()
	rawMemory := "Fixed a bug in " + projectPath + "/src/main.py."
	rolloutSummary := "Summary: fixed a bug in " + projectPath + "/src/main.py."
	if _, err := database.ExecContext(
		context.Background(),
		`INSERT INTO stage1_outputs
			(thread_id, source_updated_at, raw_memory, rollout_summary, rollout_slug, generated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
		codexThreadID, now, rawMemory, rolloutSummary, "fix-login-bug", now,
	); err != nil {
		return fmt.Errorf("insert stage1 outputs row: %w", err)
	}
	if err := database.Close(); err != nil {
		closed = true
		return fmt.Errorf("close memories database %q: %w", path, err)
	}
	closed = true
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set memories database permissions %q: %w", path, err)
	}
	return nil
}
