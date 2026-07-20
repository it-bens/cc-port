// Package sqlrewrite provides SQLite path-rewrite primitives.
package sqlrewrite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	// modernc registers the pure-Go SQLite database/sql driver.
	_ "modernc.org/sqlite"

	"github.com/it-bens/cc-port/internal/rewrite"
)

const minimumSQLiteVersion = "3.51.3"

// MaxTextValueBytes bounds a single TEXT/BLOB value RewriteTextColumn and
// CountTextColumnRO will materialize. Real path-bearing Codex metadata is a
// few KiB; this 16 MiB ceiling leaves generous headroom while refusing a
// hostile/corrupted value that could exhaust memory during a move or its
// preceding read-only dry run. Exported so a caller matching path values
// outside a single-column predicate (Codex's threads.cwd canonicalizing
// matcher, internal/tool/codex) can guard against the same class of
// oversized value with the same cap, rather than maintaining a second one.
const MaxTextValueBytes = 16 << 20

// ErrTextValueTooLarge is returned by RewriteTextColumn and CountTextColumnRO
// when a candidate TEXT/BLOB value exceeds MaxTextValueBytes; both refuse
// before materializing the value in Go memory.
var ErrTextValueTooLarge = errors.New("SQLite text value exceeds byte cap")

// FileDSN encodes path as a `file:` URL DSN carrying params as its query
// string, so a path containing '?' or other DSN-significant bytes opens the
// intended file rather than being truncated at the first such byte. Query
// keys are sorted so the DSN is deterministic across calls with the same
// params.
func FileDSN(path string, params map[string]string) string {
	fileURL := &url.URL{Scheme: "file", Path: path}
	if len(params) > 0 {
		query := url.Values{}
		for key, value := range params {
			query.Set(key, value)
		}
		fileURL.RawQuery = query.Encode()
	}
	return fileURL.String()
}

// DB is a SQLite database opened with cc-port's rewrite safety envelope.
type DB struct {
	database *sql.DB
}

// Tx is a database transaction used by rewrite operations.
type Tx struct {
	transaction *sql.Tx
}

// Open opens path with a zero busy timeout and folds its WAL into the main
// database before any caller can observe its contents.
func Open(path string) (*DB, error) {
	database, err := sql.Open("sqlite", FileDSN(path, nil))
	if err != nil {
		return nil, fmt.Errorf("open SQLite database %q: %w", path, err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	closeOnError := func(operationErr error) (*DB, error) {
		if closeErr := database.Close(); closeErr != nil {
			return nil, fmt.Errorf("%w; close SQLite database %q: %w", operationErr, path, closeErr)
		}
		return nil, operationErr
	}

	if _, err := database.ExecContext(context.Background(), "PRAGMA busy_timeout=0"); err != nil {
		return closeOnError(fmt.Errorf("set SQLite busy timeout for %q: %w", path, err))
	}
	if err := checkpointTruncate(database); err != nil {
		return closeOnError(fmt.Errorf("checkpoint SQLite database %q on open: %w", path, err))
	}

	var version string
	if err := database.QueryRowContext(context.Background(), "SELECT sqlite_version()").Scan(&version); err != nil {
		return closeOnError(fmt.Errorf("query SQLite version for %q: %w", path, err))
	}
	if err := validateSQLiteVersion(version); err != nil {
		return closeOnError(fmt.Errorf("SQLite database %q: %w", path, err))
	}

	return &DB{database: database}, nil
}

// Close closes the underlying SQLite connection pool.
func (database *DB) Close() error {
	if database == nil || database.database == nil {
		return nil
	}
	if err := database.database.Close(); err != nil {
		return fmt.Errorf("close SQLite database: %w", err)
	}
	return nil
}

// Begin starts a transaction for one or more rewrite operations.
func (database *DB) Begin() (*Tx, error) {
	if database == nil || database.database == nil {
		return nil, fmt.Errorf("begin SQLite rewrite transaction: database is nil")
	}
	transaction, err := database.database.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, fmt.Errorf("begin SQLite rewrite transaction: %w", err)
	}
	return &Tx{transaction: transaction}, nil
}

// Commit commits the transaction.
func (transaction *Tx) Commit() error {
	if transaction == nil || transaction.transaction == nil {
		return fmt.Errorf("commit SQLite rewrite transaction: transaction is nil")
	}
	if err := transaction.transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite rewrite transaction: %w", err)
	}
	return nil
}

// Rollback rolls back the transaction.
func (transaction *Tx) Rollback() error {
	if transaction == nil || transaction.transaction == nil {
		return fmt.Errorf("rollback SQLite rewrite transaction: transaction is nil")
	}
	if err := transaction.transaction.Rollback(); err != nil {
		return fmt.Errorf("rollback SQLite rewrite transaction: %w", err)
	}
	return nil
}

// CheckpointTruncate folds the WAL into the main database and truncates it.
func (database *DB) CheckpointTruncate() error {
	if database == nil || database.database == nil {
		return fmt.Errorf("checkpoint SQLite database: database is nil")
	}
	if err := checkpointTruncate(database.database); err != nil {
		return fmt.Errorf("checkpoint SQLite database: %w", err)
	}
	return nil
}

// CountPathColumnRO counts values equal to oldPath or nested below it at a
// slash boundary using the caller's read-only connection.
func CountPathColumnRO(database *sql.DB, table, column, oldPath string) (int, error) {
	if err := validatePathArguments(oldPath, oldPath); err != nil {
		return 0, err
	}
	if database == nil {
		return 0, fmt.Errorf("count SQLite path column: database is nil")
	}
	if err := requireColumns(database, table, column); err != nil {
		return 0, err
	}

	predicate, arguments := pathColumnPredicate(column, oldPath)
	// #nosec G201 -- table and column names are quoted identifiers, never values.
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", quoteIdentifier(table), predicate)
	var count int
	if err := database.QueryRowContext(context.Background(), query, arguments...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count path values in %s.%s: %w", table, column, err)
	}
	return count, nil
}

// RewritePathColumn rewrites exact and slash-boundary-prefixed path values in
// the caller's transaction.
func (database *DB) RewritePathColumn(transaction *Tx, table, column, oldPath, newPath string) (int, error) {
	if err := validatePathArguments(oldPath, newPath); err != nil {
		return 0, err
	}
	if transaction == nil || transaction.transaction == nil {
		return 0, fmt.Errorf("rewrite SQLite path column: transaction is nil")
	}
	if err := requireColumns(transaction.transaction, table, column); err != nil {
		return 0, err
	}

	predicate, predicateArguments := pathColumnPredicate(column, oldPath)
	// #nosec G201 -- table and column names are quoted identifiers, never values.
	query := fmt.Sprintf(
		"UPDATE %s SET %s = ? || substr(%s, length(?)+1) WHERE %s",
		quoteIdentifier(table), quoteIdentifier(column), quoteIdentifier(column), predicate,
	)
	arguments := append([]any{newPath, oldPath}, predicateArguments...)
	result, err := transaction.transaction.ExecContext(context.Background(), query, arguments...)
	if err != nil {
		return 0, fmt.Errorf("rewrite path values in %s.%s: %w", table, column, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count rewritten path values in %s.%s: %w", table, column, err)
	}
	return int(count), nil
}

// CountTextColumnRO counts rows whose TEXT/BLOB column contains a bounded
// reference to oldPath, refusing any candidate value larger than the shared
// per-value cap before materializing it.
func CountTextColumnRO(database *sql.DB, table, column, oldPath string) (int, error) {
	if err := validatePathArguments(oldPath, oldPath); err != nil {
		return 0, err
	}
	if database == nil {
		return 0, fmt.Errorf("count SQLite text column: database is nil")
	}
	if err := requireColumns(database, table, column); err != nil {
		return 0, err
	}

	// #nosec G201 -- table and column names are quoted identifiers, never values.
	guardQuery := fmt.Sprintf(
		"SELECT octet_length(%s) FROM %s WHERE instr(%s, ?) > 0 AND octet_length(%s) > ? LIMIT 1",
		quoteIdentifier(column), quoteIdentifier(table), quoteIdentifier(column), quoteIdentifier(column),
	)
	var byteCount int64
	switch err := database.QueryRowContext(context.Background(), guardQuery, oldPath, MaxTextValueBytes).Scan(&byteCount); {
	case err == nil:
		return 0, fmt.Errorf(
			"%w: %s.%s is %d bytes, exceeding the %d byte cap",
			ErrTextValueTooLarge, table, column, byteCount, MaxTextValueBytes,
		)
	case !errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("guard text values in %s.%s: %w", table, column, err)
	}

	// #nosec G201 -- table and column names are quoted identifiers, never values.
	selectQuery := fmt.Sprintf(
		"SELECT %s FROM %s WHERE instr(%s, ?) > 0",
		quoteIdentifier(column), quoteIdentifier(table), quoteIdentifier(column),
	)
	rows, err := database.QueryContext(context.Background(), selectQuery, oldPath)
	if err != nil {
		return 0, fmt.Errorf("count text values in %s.%s: %w", table, column, err)
	}
	defer func() { _ = rows.Close() }()

	count := 0
	for rows.Next() {
		var value any
		if err := rows.Scan(&value); err != nil {
			return 0, fmt.Errorf("read text value from %s.%s: %w", table, column, err)
		}
		matches, err := countPathInSQLiteValue(value, oldPath)
		if err != nil {
			return 0, fmt.Errorf("count text value from %s.%s: %w", table, column, err)
		}
		if matches > 0 {
			count++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("count text values in %s.%s: %w", table, column, err)
	}
	return count, nil
}

// RewriteTextColumn rewrites bounded path references in TEXT or BLOB values
// and writes each changed row back by its declared primary key.
func (database *DB) RewriteTextColumn(transaction *Tx, table, primaryKeyColumn, column, oldPath, newPath string) (int, error) {
	if err := validatePathArguments(oldPath, newPath); err != nil {
		return 0, err
	}
	if transaction == nil || transaction.transaction == nil {
		return 0, fmt.Errorf("rewrite SQLite text column: transaction is nil")
	}
	if err := requirePrimaryKeyAndColumn(transaction.transaction, table, primaryKeyColumn, column); err != nil {
		return 0, err
	}

	// #nosec G201 -- table and column names are quoted identifiers, never values.
	guardQuery := fmt.Sprintf(
		"SELECT %s, octet_length(%s) FROM %s WHERE instr(%s, ?) > 0 AND octet_length(%s) > ?",
		quoteIdentifier(primaryKeyColumn), quoteIdentifier(column), quoteIdentifier(table), quoteIdentifier(column), quoteIdentifier(column),
	)
	guardRows, err := transaction.transaction.QueryContext(context.Background(), guardQuery, oldPath, MaxTextValueBytes)
	if err != nil {
		return 0, fmt.Errorf("guard text values in %s.%s: %w", table, column, err)
	}
	if guardRows.Next() {
		var primaryKey any
		var byteCount int64
		if err := guardRows.Scan(&primaryKey, &byteCount); err != nil {
			_ = guardRows.Close()
			return 0, fmt.Errorf("read text value guard from %s.%s: %w", table, column, err)
		}
		overCapErr := fmt.Errorf(
			"%w: %s.%s at %s=%v is %d bytes, exceeding the %d byte cap",
			ErrTextValueTooLarge, table, column, primaryKeyColumn, primaryKey, byteCount, MaxTextValueBytes,
		)
		if err := guardRows.Close(); err != nil {
			return 0, fmt.Errorf("%w; close text value guard for %s.%s: %w", overCapErr, table, column, err)
		}
		return 0, overCapErr
	}
	if err := guardRows.Err(); err != nil {
		_ = guardRows.Close()
		return 0, fmt.Errorf("guard text values in %s.%s: %w", table, column, err)
	}
	if err := guardRows.Close(); err != nil {
		return 0, fmt.Errorf("close text value guard for %s.%s: %w", table, column, err)
	}

	// #nosec G201 -- table and column names are quoted identifiers, never values.
	selectQuery := fmt.Sprintf(
		"SELECT %s, %s FROM %s WHERE instr(%s, ?) > 0",
		quoteIdentifier(primaryKeyColumn), quoteIdentifier(column), quoteIdentifier(table), quoteIdentifier(column),
	)
	rows, err := transaction.transaction.QueryContext(context.Background(), selectQuery, oldPath)
	if err != nil {
		return 0, fmt.Errorf("stream text values from %s.%s: %w", table, column, err)
	}
	defer func() { _ = rows.Close() }()

	// #nosec G201 -- table and column names are quoted identifiers, never values.
	updateQuery := fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ?", quoteIdentifier(table), quoteIdentifier(column), quoteIdentifier(primaryKeyColumn))
	statement, err := transaction.transaction.PrepareContext(context.Background(), updateQuery)
	if err != nil {
		return 0, fmt.Errorf("prepare text rewrite for %s.%s: %w", table, column, err)
	}
	defer func() { _ = statement.Close() }()

	count := 0
	for rows.Next() {
		var primaryKey any
		var value any
		if err := rows.Scan(&primaryKey, &value); err != nil {
			return 0, fmt.Errorf("read text value from %s.%s: %w", table, column, err)
		}

		rewritten, replacements, err := rewriteSQLiteValue(value, oldPath, newPath)
		if err != nil {
			return 0, fmt.Errorf("rewrite text value from %s.%s: %w", table, column, err)
		}
		if replacements == 0 {
			continue
		}
		if _, err := statement.ExecContext(context.Background(), rewritten, primaryKey); err != nil {
			return 0, fmt.Errorf("write rewritten text value to %s.%s: %w", table, column, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("stream text values from %s.%s: %w", table, column, err)
	}
	return count, nil
}

// UpdateColumnsByKey updates columns on an existing row identified by its
// declared single-column primary key. It never inserts a row; callers use it
// for foreign derived stores where reconstitution belongs to the owner.
func (database *DB) UpdateColumnsByKey(transaction *Tx, table, primaryKeyColumn string, primaryKey any, values map[string]any) (int, error) {
	if transaction == nil || transaction.transaction == nil {
		return 0, fmt.Errorf("update SQLite columns by key: transaction is nil")
	}
	if len(values) == 0 {
		return 0, fmt.Errorf("update SQLite columns by key: no columns supplied")
	}
	columns := make([]string, 0, len(values))
	for column := range values {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	if err := requirePrimaryKeyAndColumns(transaction.transaction, table, primaryKeyColumn, columns...); err != nil {
		return 0, err
	}

	assignments := make([]string, 0, len(columns))
	arguments := make([]any, 0, len(columns)+1)
	for _, column := range columns {
		assignments = append(assignments, quoteIdentifier(column)+" = ?")
		arguments = append(arguments, values[column])
	}
	arguments = append(arguments, primaryKey)
	// #nosec G201 -- table and column names are quoted identifiers, never values.
	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s = ?", quoteIdentifier(table), strings.Join(assignments, ", "), quoteIdentifier(primaryKeyColumn))
	result, err := transaction.transaction.ExecContext(context.Background(), query, arguments...)
	if err != nil {
		return 0, fmt.Errorf("update columns in %s by %s: %w", table, primaryKeyColumn, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count updated rows in %s by %s: %w", table, primaryKeyColumn, err)
	}
	return int(count), nil
}

func checkpointTruncate(database *sql.DB) error {
	var busy, logFrames, checkpointedFrames int
	checkpoint := database.QueryRowContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	if err := checkpoint.Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		return fmt.Errorf("run WAL checkpoint truncate: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("run WAL checkpoint truncate: SQLite busy (%d log frames, %d checkpointed frames)", logFrames, checkpointedFrames)
	}
	return nil
}

func validateSQLiteVersion(version string) error {
	if _, err := parseSQLiteVersion(version); err != nil {
		return fmt.Errorf("parse bundled SQLite version %q: %w", version, err)
	}
	if _, err := parseSQLiteVersion(minimumSQLiteVersion); err != nil {
		return fmt.Errorf("parse minimum SQLite version %q: %w", minimumSQLiteVersion, err)
	}
	if !versionMeetsFloor(version, minimumSQLiteVersion) {
		return fmt.Errorf("bundled SQLite version %s is below required %s", version, minimumSQLiteVersion)
	}
	return nil
}

func versionMeetsFloor(bundled, floor string) bool {
	actual, actualErr := parseSQLiteVersion(bundled)
	minimum, minimumErr := parseSQLiteVersion(floor)
	if actualErr != nil || minimumErr != nil {
		return false
	}
	for index := range actual {
		if actual[index] != minimum[index] {
			return actual[index] > minimum[index]
		}
	}
	return true
}

func parseSQLiteVersion(version string) ([3]int, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return [3]int{}, fmt.Errorf("expected major.minor.patch")
	}
	var parsed [3]int
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return [3]int{}, fmt.Errorf("invalid component %q", part)
		}
		parsed[index] = value
	}
	return parsed, nil
}

func validatePathArguments(oldPath, newPath string) error {
	if oldPath == "" {
		return fmt.Errorf("SQLite path rewrite old path is empty")
	}
	if newPath == "" {
		return fmt.Errorf("SQLite path rewrite new path is empty")
	}
	return nil
}

func pathColumnPredicate(column, oldPath string) (predicate string, arguments []any) {
	quotedColumn := quoteIdentifier(column)
	predicate = fmt.Sprintf(
		"%s COLLATE BINARY = ? OR substr(%s, 1, length(?)+1) COLLATE BINARY = ? || '/'",
		quotedColumn, quotedColumn,
	)
	return predicate, []any{oldPath, oldPath, oldPath}
}

type schemaQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func requireColumns(querier schemaQuerier, table string, columns ...string) error {
	observed, err := schema(querier, table)
	if err != nil {
		return err
	}
	for _, column := range columns {
		if _, ok := observed[column]; !ok {
			return fmt.Errorf("unexpected schema for table %q: missing column %q; observed %s", table, column, formatSchema(observed))
		}
	}
	return nil
}

func requirePrimaryKeyAndColumn(querier schemaQuerier, table, primaryKeyColumn, column string) error {
	return requirePrimaryKeyAndColumns(querier, table, primaryKeyColumn, column)
}

func requirePrimaryKeyAndColumns(querier schemaQuerier, table, primaryKeyColumn string, columns ...string) error {
	observed, err := schema(querier, table)
	if err != nil {
		return err
	}
	primaryKey, ok := observed[primaryKeyColumn]
	if !ok || primaryKey.primaryKey != 1 {
		return fmt.Errorf("unexpected schema for table %q: primary key column %q is required; observed %s", table, primaryKeyColumn, formatSchema(observed))
	}
	for name, definition := range observed {
		if name != primaryKeyColumn && definition.primaryKey != 0 {
			return fmt.Errorf("unexpected schema for table %q: composite primary keys are unsupported; observed %s", table, formatSchema(observed))
		}
	}
	for _, column := range columns {
		if _, ok := observed[column]; !ok {
			return fmt.Errorf("unexpected schema for table %q: missing column %q; observed %s", table, column, formatSchema(observed))
		}
	}
	return nil
}

type columnDefinition struct {
	name       string
	typeName   string
	primaryKey int
}

func schema(querier schemaQuerier, table string) (map[string]columnDefinition, error) {
	query := fmt.Sprintf("PRAGMA table_info(%s)", quoteIdentifier(table))
	rows, err := querier.QueryContext(context.Background(), query)
	if err != nil {
		return nil, fmt.Errorf("inspect SQLite schema for table %q: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	observed := make(map[string]columnDefinition)
	for rows.Next() {
		var ordinal, notNull, primaryKey int
		var name, typeName string
		var defaultValue any
		if err := rows.Scan(&ordinal, &name, &typeName, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("read SQLite schema for table %q: %w", table, err)
		}
		observed[name] = columnDefinition{name: name, typeName: typeName, primaryKey: primaryKey}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect SQLite schema for table %q: %w", table, err)
	}
	if len(observed) == 0 {
		return nil, fmt.Errorf("unexpected schema for table %q: table is missing; observed no columns", table)
	}
	return observed, nil
}

func formatSchema(observed map[string]columnDefinition) string {
	definitions := make([]string, 0, len(observed))
	for _, definition := range observed {
		primaryKey := ""
		if definition.primaryKey != 0 {
			primaryKey = fmt.Sprintf(" primary-key-%d", definition.primaryKey)
		}
		definitions = append(definitions, fmt.Sprintf("%s %s%s", definition.name, definition.typeName, primaryKey))
	}
	sort.Strings(definitions)
	return strings.Join(definitions, ", ")
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func rewriteSQLiteValue(value any, oldPath, newPath string) (rewrittenValue any, count int, err error) {
	switch typed := value.(type) {
	case string:
		rewritten, count := rewrite.ReplacePathInBytes([]byte(typed), oldPath, newPath)
		return string(rewritten), count, nil
	case []byte:
		rewritten, count := rewrite.ReplacePathInBytes(typed, oldPath, newPath)
		return rewritten, count, nil
	default:
		return nil, 0, fmt.Errorf("expected TEXT or BLOB value, got %T", value)
	}
}

func countPathInSQLiteValue(value any, oldPath string) (int, error) {
	switch typed := value.(type) {
	case string:
		return rewrite.CountPathInBytes([]byte(typed), oldPath), nil
	case []byte:
		return rewrite.CountPathInBytes(typed, oldPath), nil
	default:
		return 0, fmt.Errorf("expected TEXT or BLOB value, got %T", value)
	}
}
