package codex

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/it-bens/cc-port/internal/sqlrewrite"
)

// Depended-on threads columns (state/migrations/0001_threads.sql,
// 0039_threads_recency_at.sql, 0040_threads_history_mode.sql): cwd,
// rollout_path, archived_at, title. Only cwd is rewritten by move; the
// others are read-only context for the future threads sidecar (§6.5,
// next bundle).
const (
	threadsTable             = "threads"
	threadsCwdColumn         = "cwd"
	agentJobsTable           = "agent_jobs"
	agentJobsIDColumn        = "id"
	agentJobsInputCSVColumn  = "input_csv_path"
	agentJobsOutputCSVColumn = "output_csv_path"
)

// stateDBKnowsProject reports whether any discovered state_*.sqlite
// database has a thread whose cwd is oldPath (exact or /-boundary prefix).
// Identity and planning use read-only SELECTs: threads.cwd uses the
// exact/prefix predicate, while agent_jobs free-text columns use
// countTextRows for boundary-aware path counting.
func stateDBKnowsProject(sqliteDir, oldPath string) (bool, error) {
	databases, err := discoverDatabases(sqliteDir, stateDBGlob)
	if err != nil {
		return false, err
	}
	for _, path := range databases {
		known, err := stateDBFileKnowsProject(path, oldPath)
		if err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
		if known {
			return true, nil
		}
	}
	return false, nil
}

func stateDBFileKnowsProject(path, oldPath string) (bool, error) {
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = database.Close() }()

	var count int
	err = database.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM threads WHERE cwd = ? OR substr(cwd, 1, length(?)+1) = ? || '/'`, oldPath, oldPath, oldPath,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// countStateDB reports how many occurrences a move would rewrite across
// every discovered state_*.sqlite database: threads.cwd plus any
// agent_jobs path column that references the project path. It uses read-only
// SELECT counts: threads.cwd uses an exact-or-prefix predicate, while the
// free-text agent_jobs columns are streamed through countTextRows so their
// boundary-aware path matches agree with Apply's rewrite logic.
func countStateDB(sqliteDir, oldPath, newPath string) (int, error) {
	databases, err := discoverDatabases(sqliteDir, stateDBGlob)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, path := range databases {
		count, err := countStateDBFile(path, oldPath, newPath)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", path, err)
		}
		total += count
	}
	return total, nil
}

func countStateDBFile(path, oldPath, _ string) (int, error) {
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = database.Close() }()

	return countStateDBReadOnly(database, oldPath)
}

func countStateDBReadOnly(database *sql.DB, oldPath string) (int, error) {
	var total int
	if err := database.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM threads WHERE cwd = ? OR substr(cwd, 1, length(?)+1) = ? || '/'`, oldPath, oldPath, oldPath,
	).Scan(&total); err != nil {
		return 0, fmt.Errorf("count threads.cwd: %w", err)
	}
	for _, column := range []string{agentJobsInputCSVColumn, agentJobsOutputCSVColumn} {
		count, err := countTextRows(database, agentJobsTable, column, oldPath)
		if err != nil {
			return 0, fmt.Errorf("count agent_jobs.%s: %w", column, err)
		}
		total += count
	}
	return total, nil
}

func rewriteThreadsAndAgentJobs(database *sqlrewrite.DB, transaction *sqlrewrite.Tx, oldPath, newPath string) (int, error) {
	count, err := database.RewritePathColumn(transaction, threadsTable, threadsCwdColumn, oldPath, newPath)
	if err != nil {
		return 0, fmt.Errorf("rewrite threads.cwd: %w", err)
	}

	for _, column := range []string{agentJobsInputCSVColumn, agentJobsOutputCSVColumn} {
		rewritten, err := database.RewriteTextColumn(transaction, agentJobsTable, agentJobsIDColumn, column, oldPath, newPath)
		if err != nil {
			return 0, fmt.Errorf("rewrite agent_jobs.%s: %w", column, err)
		}
		count += rewritten
	}
	return count, nil
}
