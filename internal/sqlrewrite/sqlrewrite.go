// Package sqlrewrite provides SQLite path-rewrite primitives.
package sqlrewrite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"

	// modernc registers the pure-Go SQLite database/sql driver.
	_ "modernc.org/sqlite"

	"github.com/it-bens/cc-port/internal/rewrite"
)

const minimumSQLiteVersion = "3.51.3"

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

// CountTextColumnRO counts rows whose TEXT/BLOB column contains a
// boundary-aware reference to oldPath.
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

// RewriteTextColumn rewrites path references in TEXT or BLOB values
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
// declared single-column primary key, and only while every column in
// expected still holds its expected value byte for byte. A nil or empty
// expected adds no predicate. A row that does not match counts as zero. It
// never inserts a row; callers use it for foreign derived stores where
// reconstitution belongs to the owner.
func (database *DB) UpdateColumnsByKey(
	transaction *Tx, table, primaryKeyColumn string, primaryKey any, values, expected map[string]any,
) (int, error) {
	if transaction == nil || transaction.transaction == nil {
		return 0, fmt.Errorf("update SQLite columns by key: transaction is nil")
	}
	if len(values) == 0 {
		return 0, fmt.Errorf("update SQLite columns by key: no columns supplied")
	}
	if err := refuseNilExpectedValues("update SQLite columns by key", expected); err != nil {
		return 0, err
	}
	columns := sortedColumns(values)
	required := append(sortedColumns(expected), columns...)
	if err := requirePrimaryKeyAndColumns(transaction.transaction, table, primaryKeyColumn, required...); err != nil {
		return 0, err
	}
	return updateColumnsWhere(transaction, table, quoteIdentifier(primaryKeyColumn), primaryKey, columns, values, expected)
}

// UpdateColumnsByRowID updates columns on an existing row identified by its
// SQLite rowid, for a table whose declared primary key is composite, and only
// while every column in expected still holds its expected value byte for
// byte. expected must name at least one column: SQLite can hand a freed
// rowid to a different row. A row that does not match counts as zero. It
// refuses a WITHOUT ROWID table and a table declaring a column named rowid,
// and never inserts a row.
func (database *DB) UpdateColumnsByRowID(transaction *Tx, table string, rowID int64, values, expected map[string]any) (int, error) {
	if transaction == nil || transaction.transaction == nil {
		return 0, fmt.Errorf("update SQLite columns by rowid: transaction is nil")
	}
	if len(values) == 0 {
		return 0, fmt.Errorf("update SQLite columns by rowid: no columns supplied")
	}
	if len(expected) == 0 {
		return 0, fmt.Errorf("update SQLite columns by rowid: no expected values supplied")
	}
	if err := refuseNilExpectedValues("update SQLite columns by rowid", expected); err != nil {
		return 0, err
	}
	columns := sortedColumns(values)
	required := append(sortedColumns(expected), columns...)
	if err := requireRowIDTableAndColumns(transaction.transaction, table, required...); err != nil {
		return 0, err
	}
	return updateColumnsWhere(transaction, table, "rowid", rowID, columns, values, expected)
}

// refuseNilExpectedValues rejects a nil expected value, untyped or a typed
// nil such as a nil pointer: either binds SQL NULL, and = NULL never
// matches, so the guarded update would silently write nothing.
func refuseNilExpectedValues(operation string, expected map[string]any) error {
	for _, column := range sortedColumns(expected) {
		if isNil(expected[column]) {
			return fmt.Errorf("%s: expected value for column %q is nil", operation, column)
		}
	}
	return nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return reflected.IsNil()
	default:
		return false
	}
}

func sortedColumns(values map[string]any) []string {
	columns := make([]string, 0, len(values))
	for column := range values {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	return columns
}

// updateColumnsWhere runs one UPDATE keyed on keyExpression. keyExpression is
// either a quoted identifier or the literal rowid, never a caller value. Each
// expected column adds a COLLATE BINARY equality predicate, so a column
// declared with a case-insensitive collation cannot let a byte-different
// current value pass the guard.
func updateColumnsWhere(
	transaction *Tx, table, keyExpression string, key any, columns []string, values, expected map[string]any,
) (int, error) {
	assignments := make([]string, 0, len(columns))
	arguments := make([]any, 0, len(columns)+1+len(expected))
	for _, column := range columns {
		assignments = append(assignments, quoteIdentifier(column)+" = ?")
		arguments = append(arguments, values[column])
	}
	predicates := []string{keyExpression + " = ?"}
	arguments = append(arguments, key)
	for _, column := range sortedColumns(expected) {
		predicates = append(predicates, quoteIdentifier(column)+" COLLATE BINARY = ?")
		arguments = append(arguments, expected[column])
	}
	// #nosec G201 -- table and column names are quoted identifiers, never values.
	query := fmt.Sprintf(
		"UPDATE %s SET %s WHERE %s",
		quoteIdentifier(table), strings.Join(assignments, ", "), strings.Join(predicates, " AND "),
	)
	result, err := transaction.transaction.ExecContext(context.Background(), query, arguments...)
	if err != nil {
		return 0, fmt.Errorf("update columns in %s by %s: %w", table, keyExpression, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count updated rows in %s by %s: %w", table, keyExpression, err)
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

// RequirePrimaryKeyAndColumns checks, on the caller's read-only connection,
// that table declares primaryKeyColumn as its single-column primary key and
// carries every column, failing with the observed schema exactly as
// UpdateColumnsByKey does. A read-only plan calls it so a dry run refuses the
// schema its apply would refuse.
func RequirePrimaryKeyAndColumns(database *sql.DB, table, primaryKeyColumn string, columns ...string) error {
	if database == nil {
		return fmt.Errorf("require SQLite schema: database is nil")
	}
	return requirePrimaryKeyAndColumns(database, table, primaryKeyColumn, columns...)
}

// RequireRowIDTableAndColumns checks, on the caller's read-only connection,
// that table is an ordinary rowid table (not WITHOUT ROWID, and declaring no
// column that shadows rowid) and carries every column, failing with the
// observed schema exactly as UpdateColumnsByRowID does. A read-only plan
// calls it so a dry run refuses the schema its apply would refuse.
func RequireRowIDTableAndColumns(database *sql.DB, table string, columns ...string) error {
	if database == nil {
		return fmt.Errorf("require SQLite schema: database is nil")
	}
	return requireRowIDTableAndColumns(database, table, columns...)
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

// requireRowIDTableAndColumns reads the table's wr flag from pragma_table_list,
// which SQLite sets from the parsed schema, rather than matching "WITHOUT
// ROWID" in the stored CREATE text or probing SELECT rowid: a declared column
// named rowid answers that probe on a WITHOUT ROWID table and shadows the
// real rowid in WHERE rowid = ? on a rowid table, so such a column is
// refused. A column named oid or _rowid_ shadows only its own name.
func requireRowIDTableAndColumns(querier schemaQuerier, table string, columns ...string) error {
	observed, err := schema(querier, table)
	if err != nil {
		return err
	}
	for name := range observed {
		if strings.EqualFold(name, "rowid") {
			return fmt.Errorf("unexpected schema for table %q: declared column %q shadows the rowid; observed %s", table, name, formatSchema(observed))
		}
	}
	rows, err := querier.QueryContext(context.Background(), "SELECT type, wr FROM pragma_table_list(?)", table)
	if err != nil {
		return fmt.Errorf("inspect SQLite table kind for table %q: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	var kinds []string
	withoutRowID := false
	for rows.Next() {
		var kind string
		var wr int
		if err := rows.Scan(&kind, &wr); err != nil {
			return fmt.Errorf("read SQLite table kind for table %q: %w", table, err)
		}
		kinds = append(kinds, kind)
		withoutRowID = withoutRowID || wr != 0
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect SQLite table kind for table %q: %w", table, err)
	}
	if len(kinds) != 1 || kinds[0] != "table" || withoutRowID {
		return fmt.Errorf(
			"unexpected schema for table %q: an ordinary rowid table is required; observed kinds %v, without rowid %t",
			table, kinds, withoutRowID,
		)
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
