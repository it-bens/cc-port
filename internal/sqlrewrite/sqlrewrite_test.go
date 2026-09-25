package sqlrewrite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundledSQLiteVersionMeetsRequiredFloor(t *testing.T) {
	database := openSQLite(t, filepath.Join(t.TempDir(), "version.sqlite"))
	var version string
	require.NoError(t, database.QueryRowContext(context.Background(), "SELECT sqlite_version()").Scan(&version))

	assert.GreaterOrEqual(t, sqliteVersionNumber(t, version), sqliteVersionNumber(t, "3.51.3"))
}

func TestOpenRefusesBusyWriterImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.sqlite")
	database := openSQLite(t, path)
	require.NoError(t, prepareWAL(database))
	require.NoError(t, database.Close())

	writerDatabase := openSQLite(t, path)
	writerConnection, err := writerDatabase.Conn(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, writerConnection.Close()) })
	require.NoError(t, executeContext(writerConnection, "BEGIN IMMEDIATE"))
	t.Cleanup(func() { _, _ = writerConnection.ExecContext(context.Background(), "ROLLBACK") })

	// The busy-wait a regression would introduce happens inside SQLite's C
	// busy loop, not on a Go clock this test can inject, and a connection's
	// busy_timeout cannot be read back through database/sql. A bounded
	// wall-clock measurement is the only mechanism available to prove Open
	// refused immediately rather than waiting out a nonzero busy_timeout.
	started := time.Now()
	_, err = Open(path)
	elapsed := time.Since(started)

	require.Error(t, err)
	assert.True(t, strings.Contains(strings.ToLower(err.Error()), "busy") || strings.Contains(strings.ToLower(err.Error()), "locked"))
	assert.Less(t, elapsed, time.Second, "busy_timeout=0 must make Open on a busy database fail immediately, not wait")
}

func TestOpenFoldsWALBeforeMainDatabaseIsObserved(t *testing.T) {
	temporaryDirectory := t.TempDir()
	path := filepath.Join(temporaryDirectory, "checkpoint.sqlite")
	writer := openSQLite(t, path)
	require.NoError(t, prepareWAL(writer))
	require.NoError(t, execute(writer, "CREATE TABLE entries (value TEXT)"))
	require.NoError(t, checkpoint(writer))
	require.NoError(t, execute(writer, "INSERT INTO entries (value) VALUES ('only-in-wal')"))

	walInfo, err := os.Stat(path + "-wal")
	require.NoError(t, err)
	require.Positive(t, walInfo.Size())

	beforePath := copyMainDatabase(t, path, filepath.Join(temporaryDirectory, "before.sqlite"))
	assert.Equal(t, 0, entryCount(t, beforePath))

	rewriter, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, rewriter.Close())

	afterPath := copyMainDatabase(t, path, filepath.Join(temporaryDirectory, "after.sqlite"))
	assert.Equal(t, 1, entryCount(t, afterPath))
}

func TestFileDSNEncodesQuestionMarkPath(t *testing.T) {
	weirdDir := filepath.Join(t.TempDir(), "dir?name")
	require.NoError(t, os.MkdirAll(weirdDir, 0o750))
	path := filepath.Join(weirdDir, "test.sqlite")

	writer, err := sql.Open("sqlite", FileDSN(path, nil))
	require.NoError(t, err)
	require.NoError(t, execute(writer, "CREATE TABLE entries (value TEXT)"))
	require.NoError(t, execute(writer, "INSERT INTO entries (value) VALUES ('encoded')"))
	require.NoError(t, writer.Close())

	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "FileDSN must address the literal path, not a prefix truncated at '?'")

	reader, err := sql.Open("sqlite", FileDSN(path, nil))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })
	var value string
	require.NoError(t, reader.QueryRowContext(context.Background(), "SELECT value FROM entries").Scan(&value))
	assert.Equal(t, "encoded", value)
}

func TestRewriteTextColumnPreservesTextAndBlobStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "text.sqlite")
	database := openSQLite(t, path)
	require.NoError(t, prepareWAL(database))
	require.NoError(t, execute(database, "CREATE TABLE documents (id INTEGER PRIMARY KEY, text_content TEXT, blob_content BLOB)"))
	oldPath := "/Users/test/Projects/my_app"
	newPath := "/Users/test/Projects/new_app"
	binaryPrefix := []byte{0x00, 0xff, 0xfe, 0x00}
	binarySuffix := []byte{0xfe, 0xff, 0x00}
	blobInput := append(append(append([]byte(nil), binaryPrefix...), []byte(oldPath+"/notes")...), binarySuffix...)
	blobExpected := append(append(append([]byte(nil), binaryPrefix...), []byte(newPath+"/notes")...), binarySuffix...)
	require.NoError(t, database.Close())

	insertDatabase := openSQLite(t, path)
	require.NoError(t, execute(
		insertDatabase,
		"INSERT INTO documents (id, text_content, blob_content) VALUES (?, ?, ?)",
		1,
		"text "+oldPath+"/notes",
		blobInput,
	))
	require.NoError(t, insertDatabase.Close())

	rewriter, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
	transaction, err := rewriter.Begin()
	require.NoError(t, err)
	textCount, err := rewriter.RewriteTextColumn(transaction, "documents", "id", "text_content", oldPath, newPath)
	require.NoError(t, err)
	blobCount, err := rewriter.RewriteTextColumn(transaction, "documents", "id", "blob_content", oldPath, newPath)
	require.NoError(t, err)
	require.NoError(t, transaction.Commit())
	require.NoError(t, rewriter.CheckpointTruncate())

	assert.Equal(t, 1, textCount)
	assert.Equal(t, 1, blobCount)

	verification := openSQLite(t, path)
	var textValue, textType, blobType string
	var blobValue []byte
	query := "SELECT text_content, typeof(text_content), blob_content, typeof(blob_content) FROM documents WHERE id = 1"
	require.NoError(t, verification.QueryRowContext(context.Background(), query).Scan(&textValue, &textType, &blobValue, &blobType))
	assert.Equal(t, "text "+newPath+"/notes", textValue)
	assert.Equal(t, "text", textType)
	require.Len(t, blobValue, len(blobExpected))
	assert.Equal(t, blobExpected, blobValue)
	assert.Equal(t, binaryPrefix, blobValue[:len(binaryPrefix)])
	assert.Equal(t, binarySuffix, blobValue[len(blobValue)-len(binarySuffix):])
	assert.Equal(t, "blob", blobType)
}

func TestUpdateColumnsByKeyUpdatesExistingRowWithoutInsert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.sqlite")
	database := openSQLite(t, path)
	require.NoError(t, prepareWAL(database))
	require.NoError(t, execute(database, "CREATE TABLE threads (id TEXT PRIMARY KEY, title TEXT, archived_at INTEGER)"))
	require.NoError(t, execute(database, "INSERT INTO threads (id, title, archived_at) VALUES (?, ?, ?)", "present", "old", nil))
	require.NoError(t, database.Close())

	rewriter, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
	transaction, err := rewriter.Begin()
	require.NoError(t, err)
	updated, err := rewriter.UpdateColumnsByKey(transaction, "threads", "id", "present", map[string]any{"title": "new", "archived_at": 42}, nil)
	require.NoError(t, err)
	absent, err := rewriter.UpdateColumnsByKey(transaction, "threads", "id", "missing",
		map[string]any{"title": "never inserted", "archived_at": 42}, nil)
	require.NoError(t, err)
	require.NoError(t, transaction.Commit())

	assert.Equal(t, 1, updated)
	assert.Zero(t, absent)
	check := openSQLite(t, path)
	t.Cleanup(func() { require.NoError(t, check.Close()) })
	var title string
	var archivedAt int
	err = check.QueryRowContext(
		context.Background(), "SELECT title, archived_at FROM threads WHERE id = 'present'",
	).Scan(&title, &archivedAt)
	require.NoError(t, err)
	assert.Equal(t, "new", title)
	assert.Equal(t, 42, archivedAt)
	var rows int
	require.NoError(t, check.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM threads").Scan(&rows))
	assert.Equal(t, 1, rows)
}

func TestUpdateColumnsByRowIDUpdatesCompositeKeyRowWithoutInsert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rowid.sqlite")
	database := openSQLite(t, path)
	require.NoError(t, prepareWAL(database))
	require.NoError(t, execute(database,
		"CREATE TABLE project_roots (project_id TEXT NOT NULL, position INTEGER NOT NULL, path TEXT NOT NULL, PRIMARY KEY (project_id, position))"))
	require.NoError(t, execute(database, "INSERT INTO project_roots (project_id, position, path) VALUES (?, ?, ?)",
		"primary-project", 0, "/Users/test/Projects/old-root"))
	require.NoError(t, execute(database, "INSERT INTO project_roots (project_id, position, path) VALUES (?, ?, ?)",
		"primary-project", 1, "/Users/test/Projects/other-root"))
	var targetRowID int64
	require.NoError(t, database.QueryRowContext(context.Background(),
		"SELECT rowid FROM project_roots WHERE position = 0").Scan(&targetRowID))
	require.NoError(t, database.Close())

	rewriter, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
	transaction, err := rewriter.Begin()
	require.NoError(t, err)
	updated, err := rewriter.UpdateColumnsByRowID(transaction, "project_roots", targetRowID,
		map[string]any{"path": "/Users/test/Projects/new-root"}, map[string]any{"path": "/Users/test/Projects/old-root"})
	require.NoError(t, err)
	require.NoError(t, transaction.Commit())

	assert.Equal(t, 1, updated)
	check := openSQLite(t, path)
	rows, err := check.QueryContext(context.Background(), "SELECT path FROM project_roots ORDER BY position")
	require.NoError(t, err)
	var paths []string
	for rows.Next() {
		var rootPath string
		require.NoError(t, rows.Scan(&rootPath))
		paths = append(paths, rootPath)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	assert.Equal(t, []string{"/Users/test/Projects/new-root", "/Users/test/Projects/other-root"}, paths)
}

func TestUpdateColumnsByRowIDReportsZeroForAbsentRowWithoutInsert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rowid-absent.sqlite")
	database := openSQLite(t, path)
	require.NoError(t, execute(database,
		"CREATE TABLE project_roots (project_id TEXT NOT NULL, position INTEGER NOT NULL, path TEXT NOT NULL, PRIMARY KEY (project_id, position))"))
	require.NoError(t, execute(database, "INSERT INTO project_roots (project_id, position, path) VALUES (?, ?, ?)",
		"primary-project", 0, "/Users/test/Projects/old-root"))
	require.NoError(t, database.Close())

	rewriter, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
	transaction, err := rewriter.Begin()
	require.NoError(t, err)
	const absentRowID = 999
	updated, err := rewriter.UpdateColumnsByRowID(transaction, "project_roots", absentRowID,
		map[string]any{"path": "/Users/test/Projects/never-inserted"}, map[string]any{"path": "/Users/test/Projects/old-root"})
	require.NoError(t, err)
	require.NoError(t, transaction.Commit())

	assert.Zero(t, updated)
	check := openSQLite(t, path)
	var rowCount int
	require.NoError(t, check.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM project_roots").Scan(&rowCount))
	assert.Equal(t, 1, rowCount)
}

func TestUpdateColumnsByRowIDWritesOnlyWhileExpectedValueHolds(t *testing.T) {
	const plannedPath = "/Users/test/Projects/old-root"
	cases := []struct {
		name        string
		currentPath string
		wantUpdated int
		wantPath    string
	}{
		{name: "value unchanged since plan", currentPath: plannedPath, wantUpdated: 1, wantPath: "/Users/test/Projects/new-root"},
		{name: "value changed since plan", currentPath: "/Users/test/Projects/other-root", wantUpdated: 0, wantPath: "/Users/test/Projects/other-root"},
		{
			name: "value changed only in letter case", currentPath: "/USERS/TEST/PROJECTS/OLD-ROOT",
			wantUpdated: 0, wantPath: "/USERS/TEST/PROJECTS/OLD-ROOT",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "guarded.sqlite")
			database := openSQLite(t, path)
			require.NoError(t, execute(database,
				"CREATE TABLE project_roots (project_id TEXT NOT NULL, position INTEGER NOT NULL, path TEXT NOT NULL COLLATE NOCASE, "+
					"PRIMARY KEY (project_id, position))"))
			require.NoError(t, execute(database, "INSERT INTO project_roots (project_id, position, path) VALUES (?, ?, ?)",
				"primary-project", 0, testCase.currentPath))
			rewriter, err := Open(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
			transaction, err := rewriter.Begin()
			require.NoError(t, err)

			updated, err := rewriter.UpdateColumnsByRowID(transaction, "project_roots", 1,
				map[string]any{"path": "/Users/test/Projects/new-root"}, map[string]any{"path": plannedPath})
			require.NoError(t, err)
			require.NoError(t, transaction.Commit())

			assert.Equal(t, testCase.wantUpdated, updated)
			var storedPath string
			require.NoError(t, database.QueryRowContext(context.Background(), "SELECT path FROM project_roots").Scan(&storedPath))
			assert.Equal(t, testCase.wantPath, storedPath)
		})
	}
}

func TestUpdateColumnsByKeyWritesOnlyWhileExpectedValueHolds(t *testing.T) {
	const plannedCWD = "/Users/test/Projects/old-project"
	cases := []struct {
		name        string
		currentCWD  string
		wantUpdated int
		wantCWD     string
	}{
		{name: "value unchanged since plan", currentCWD: plannedCWD, wantUpdated: 1, wantCWD: "/Users/test/Projects/new-project"},
		{name: "value changed since plan", currentCWD: "/Users/test/Projects/other-project", wantUpdated: 0, wantCWD: "/Users/test/Projects/other-project"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "guarded-key.sqlite")
			database := openSQLite(t, path)
			require.NoError(t, execute(database, "CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT NOT NULL)"))
			require.NoError(t, execute(database, "INSERT INTO threads (id, cwd) VALUES (?, ?)", "primary-session", testCase.currentCWD))
			rewriter, err := Open(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
			transaction, err := rewriter.Begin()
			require.NoError(t, err)

			updated, err := rewriter.UpdateColumnsByKey(transaction, "threads", "id", "primary-session",
				map[string]any{"cwd": "/Users/test/Projects/new-project"}, map[string]any{"cwd": plannedCWD})
			require.NoError(t, err)
			require.NoError(t, transaction.Commit())

			assert.Equal(t, testCase.wantUpdated, updated)
			var storedCWD string
			require.NoError(t, database.QueryRowContext(context.Background(), "SELECT cwd FROM threads").Scan(&storedCWD))
			assert.Equal(t, testCase.wantCWD, storedCWD)
		})
	}
}

func TestUpdateColumnsByRowIDRefusesMissingExpectedValue(t *testing.T) {
	cases := []struct {
		name     string
		expected map[string]any
		wantErr  string
	}{
		{name: "no expected map", expected: nil, wantErr: "update SQLite columns by rowid: no expected values supplied"},
		{name: "empty expected map", expected: map[string]any{}, wantErr: "update SQLite columns by rowid: no expected values supplied"},
		{
			name: "nil expected value", expected: map[string]any{"path": nil},
			wantErr: `update SQLite columns by rowid: expected value for column "path" is nil`,
		},
		{
			name: "typed nil expected value", expected: map[string]any{"path": (*string)(nil)},
			wantErr: `update SQLite columns by rowid: expected value for column "path" is nil`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unguarded.sqlite")
			database := openSQLite(t, path)
			require.NoError(t, execute(database,
				"CREATE TABLE project_roots (project_id TEXT NOT NULL, position INTEGER NOT NULL, path TEXT NOT NULL, PRIMARY KEY (project_id, position))"))
			require.NoError(t, execute(database, "INSERT INTO project_roots (project_id, position, path) VALUES (?, ?, ?)",
				"primary-project", 0, "/Users/test/Projects/old-root"))
			rewriter, err := Open(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
			transaction, err := rewriter.Begin()
			require.NoError(t, err)
			t.Cleanup(func() { _ = transaction.Rollback() })

			_, err = rewriter.UpdateColumnsByRowID(transaction, "project_roots", 1,
				map[string]any{"path": "/Users/test/Projects/new-root"}, testCase.expected)

			require.EqualError(t, err, testCase.wantErr)
		})
	}
}

func TestUpdateColumnsByRowIDAddressesRealRowIDBesideOidAndUnderscoreRowIDColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rowid-aliases.sqlite")
	database := openSQLite(t, path)
	require.NoError(t, execute(database, "CREATE TABLE project_roots (oid TEXT, _rowid_ TEXT, path TEXT NOT NULL)"))
	require.NoError(t, execute(database, "INSERT INTO project_roots (oid, _rowid_, path) VALUES (?, ?, ?)",
		"2", "2", "/Users/test/Projects/first-root"))
	require.NoError(t, execute(database, "INSERT INTO project_roots (oid, _rowid_, path) VALUES (?, ?, ?)",
		"1", "1", "/Users/test/Projects/second-root"))
	rewriter, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
	transaction, err := rewriter.Begin()
	require.NoError(t, err)

	updated, err := rewriter.UpdateColumnsByRowID(transaction, "project_roots", 2,
		map[string]any{"path": "/Users/test/Projects/new-root"}, map[string]any{"path": "/Users/test/Projects/second-root"})
	require.NoError(t, err)
	require.NoError(t, transaction.Commit())

	assert.Equal(t, 1, updated)
	var paths []string
	rows, err := database.QueryContext(context.Background(), "SELECT path FROM project_roots ORDER BY oid DESC")
	require.NoError(t, err)
	for rows.Next() {
		var rootPath string
		require.NoError(t, rows.Scan(&rootPath))
		paths = append(paths, rootPath)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	assert.Equal(t, []string{"/Users/test/Projects/first-root", "/Users/test/Projects/new-root"}, paths)
}

func TestUpdateColumnsByRowIDRefusesUnsupportedSchema(t *testing.T) {
	cases := []struct {
		name    string
		schema  string
		wantErr string
	}{
		{
			name:   "missing column",
			schema: "CREATE TABLE project_roots (project_id TEXT NOT NULL, position INTEGER NOT NULL, PRIMARY KEY (project_id, position))",
			wantErr: `unexpected schema for table "project_roots": missing column "path"; ` +
				`observed position INTEGER primary-key-2, project_id TEXT primary-key-1`,
		},
		{
			name: "without rowid table",
			schema: "CREATE TABLE project_roots (project_id TEXT NOT NULL, position INTEGER NOT NULL, path TEXT NOT NULL, " +
				"PRIMARY KEY (project_id, position)) WITHOUT ROWID",
			wantErr: `unexpected schema for table "project_roots": an ordinary rowid table is required; observed kinds [table], without rowid true`,
		},
		{
			name:    "column shadowing the rowid",
			schema:  "CREATE TABLE project_roots (RowID TEXT PRIMARY KEY, path TEXT NOT NULL)",
			wantErr: `unexpected schema for table "project_roots": declared column "RowID" shadows the rowid; observed RowID TEXT primary-key-1, path TEXT`,
		},
		{
			name:    "view",
			schema:  "CREATE TABLE roots_source (path TEXT); CREATE VIEW project_roots AS SELECT path FROM roots_source",
			wantErr: `unexpected schema for table "project_roots": an ordinary rowid table is required; observed kinds [view], without rowid false`,
		},
		{
			name:    "virtual table",
			schema:  "CREATE VIRTUAL TABLE project_roots USING fts5(path)",
			wantErr: `unexpected schema for table "project_roots": an ordinary rowid table is required; observed kinds [virtual], without rowid false`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "refused.sqlite")
			database := openSQLite(t, path)
			require.NoError(t, execute(database, testCase.schema))
			rewriter, err := Open(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
			transaction, err := rewriter.Begin()
			require.NoError(t, err)
			t.Cleanup(func() { _ = transaction.Rollback() })

			_, err = rewriter.UpdateColumnsByRowID(transaction, "project_roots", 1,
				map[string]any{"path": "/Users/test/Projects/new-root"}, map[string]any{"path": "/Users/test/Projects/old-root"})

			require.EqualError(t, err, testCase.wantErr)
		})
	}
}

func TestVersionMeetsFloor(t *testing.T) {
	cases := []struct {
		name    string
		bundled string
		floor   string
		want    bool
	}{
		{name: "below patch", bundled: "3.51.2", floor: "3.51.3", want: false},
		{name: "at floor", bundled: "3.51.3", floor: "3.51.3", want: true},
		{name: "above minor", bundled: "3.52.0", floor: "3.51.3", want: true},
		{name: "above major", bundled: "4.0.0", floor: "3.99.99", want: true},
		{name: "below major", bundled: "2.99.99", floor: "3.0.0", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, versionMeetsFloor(testCase.bundled, testCase.floor))
		})
	}
}

func openSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	return database
}

func prepareWAL(database *sql.DB) error {
	if _, err := database.ExecContext(context.Background(), "PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}
	return nil
}

func execute(database *sql.DB, query string, arguments ...any) error {
	_, err := database.ExecContext(context.Background(), query, arguments...)
	return err
}

func executeContext(connection *sql.Conn, query string, arguments ...any) error {
	_, err := connection.ExecContext(context.Background(), query, arguments...)
	return err
}

func checkpoint(database *sql.DB) error {
	var busy, logFrames, checkpointedFrames int
	checkpoint := database.QueryRowContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	if err := checkpoint.Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		return fmt.Errorf("checkpoint WAL: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("checkpoint WAL: busy")
	}
	return nil
}

func copyMainDatabase(t *testing.T, source, destination string) string {
	t.Helper()
	data, err := os.ReadFile(source) //nolint:gosec // G304: caller uses t.TempDir paths.
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(destination, data, 0o600)) //nolint:gosec // G703: caller uses t.TempDir paths.
	return destination
}

func entryCount(t *testing.T, path string) int {
	t.Helper()
	database := openSQLite(t, path)
	var count int
	require.NoError(t, database.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM entries").Scan(&count))
	return count
}

func sqliteVersionNumber(t *testing.T, version string) int {
	t.Helper()
	var major, minor, patch int
	_, err := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch)
	require.NoError(t, err)
	return major*1_000_000 + minor*1_000 + patch
}
