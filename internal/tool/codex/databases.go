package codex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"modernc.org/sqlite"

	"github.com/it-bens/cc-port/internal/rewrite"
)

func openReadOnlyDatabase(path string) (*sql.DB, error) {
	databaseURL := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String()
	database, err := sql.Open("sqlite", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open read-only SQLite database %s: %w", path, err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	return database, nil
}

// Database filename glob patterns. Codex's generation suffix can bump
// (state_5.sqlite today; a future binary may write state_6.sqlite), so
// every discovery site globs rather than pinning a literal filename
// (state/src/lib.rs:97-100).
const (
	stateDBGlob    = "state_*.sqlite"
	memoriesDBGlob = "memories_*.sqlite"
	goalsDBGlob    = "goals_*.sqlite"
	logsDBGlob     = "logs_*.sqlite"
	sqliteBusyCode = 5 // SQLite's stable, documented SQLITE_BUSY result code.
	walSuffix      = "-wal"
	shmSuffix      = "-shm"
)

// discoverDatabases globs sqliteDir for every file matching pattern,
// returning matches in sorted order.
func discoverDatabases(sqliteDir, pattern string) ([]string, error) {
	entries, err := os.ReadDir(sqliteDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read SQLite directory %s: %w", sqliteDir, err)
	}
	var matches []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matched, matchErr := filepath.Match(pattern, entry.Name())
		if matchErr != nil {
			return nil, fmt.Errorf("match database pattern %s: %w", pattern, matchErr)
		}
		if matched {
			matches = append(matches, filepath.Join(sqliteDir, entry.Name()))
		}
	}
	sort.Strings(matches)
	return matches, nil
}

// allDatabasePaths returns every discovered database file across all four
// generation-suffixed families, in a stable order (state, memories, goals,
// logs, each internally sorted).
func (workspace *Workspace) allDatabasePaths() ([]string, error) {
	var all []string
	for _, pattern := range []string{stateDBGlob, memoriesDBGlob, goalsDBGlob, logsDBGlob} {
		matches, err := discoverDatabases(workspace.home.SQLiteDir, pattern)
		if err != nil {
			return nil, err
		}
		all = append(all, matches...)
	}
	return all, nil
}

// probeDatabaseBusy opens path with a zero busy timeout and attempts a
// BEGIN IMMEDIATE, the same lock class Codex's own writers take. A
// SQLITE_BUSY result means a live writer holds the database; any other
// failure is a read failure, not evidence, and is returned as an error.
func probeDatabaseBusy(path string) (busy bool, err error) {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)

	ctx := context.Background()
	if _, execErr := database.ExecContext(ctx, "PRAGMA busy_timeout=0"); execErr != nil {
		return false, fmt.Errorf("set busy_timeout for %s: %w", path, execErr)
	}
	if _, execErr := database.ExecContext(ctx, "BEGIN IMMEDIATE"); execErr != nil {
		if isSQLiteBusy(execErr) {
			return true, nil
		}
		return false, fmt.Errorf("begin immediate probe on %s: %w", path, execErr)
	}
	if _, execErr := database.ExecContext(ctx, "ROLLBACK"); execErr != nil {
		return false, fmt.Errorf("rollback busy probe on %s: %w", path, execErr)
	}
	return false, nil
}

func isSQLiteBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteBusyCode
}

func countTextRows(database *sql.DB, table, column, oldPath string) (int, error) {
	if err := requireTableColumn(database, table, column); err != nil {
		return 0, err
	}
	// #nosec G201 -- table and column names are adapter constants, not user input.
	query := fmt.Sprintf(`SELECT %q FROM %q WHERE instr(%q, ?) > 0`, column, table, column)
	rows, err := database.QueryContext(context.Background(), query, oldPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var value any
		if err := rows.Scan(&value); err != nil {
			return 0, err
		}
		switch typed := value.(type) {
		case string:
			if rewrite.CountPathInBytes([]byte(typed), oldPath) > 0 {
				count++
			}
		case []byte:
			if rewrite.CountPathInBytes(typed, oldPath) > 0 {
				count++
			}
		default:
			return 0, fmt.Errorf("expected TEXT or BLOB value, got %T", value)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

func requireTableColumn(database *sql.DB, table, column string) error {
	// #nosec G201 -- table is an adapter constant, not user input.
	query := fmt.Sprintf(`PRAGMA table_info(%q)`, table)
	rows, err := database.QueryContext(context.Background(), query)
	if err != nil {
		return fmt.Errorf("read schema for %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	var observed []string
	for rows.Next() {
		var columnIndex, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&columnIndex, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("read schema for %s: %w", table, err)
		}
		observed = append(observed, name)
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read schema for %s: %w", table, err)
	}
	if len(observed) == 0 {
		return fmt.Errorf("required column %s.%s is missing (observed schema: table absent)", table, column)
	}
	return fmt.Errorf("required column %s.%s is missing (observed columns: %s)", table, column, strings.Join(observed, ", "))
}

func goalsDatabaseHasRows(path string) (bool, error) {
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = database.Close() }()
	const userTablesQuery = `SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != '_sqlx_migrations'`
	rows, err := database.QueryContext(context.Background(), userTablesQuery)
	if err != nil {
		return false, fmt.Errorf("list user tables: %w", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("read user table name: %w", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, fmt.Errorf("stream user tables: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close user table list: %w", err)
	}
	for _, table := range tables {
		// #nosec G201 -- table name came from SQLite's own sqlite_master.
		query := fmt.Sprintf(`SELECT 1 FROM %q LIMIT 1`, table)
		var value int
		err := database.QueryRowContext(context.Background(), query).Scan(&value)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("read user table %s: %w", table, err)
		}
	}
	return false, nil
}
