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

	"github.com/it-bens/cc-port/internal/rewrite"
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

func TestRewriteTextColumnRefusesOversizedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.sqlite")
	database := openSQLite(t, path)
	require.NoError(t, prepareWAL(database))
	require.NoError(t, execute(database, "CREATE TABLE documents (id TEXT PRIMARY KEY, text_content TEXT)"))
	withinCapOldPath := "/a/within-cap"
	withinCapNewPath := "/a/rewritten"
	overCapOldPath := "/a/over-cap"
	overCapNewPath := "/a/never-written"
	overCapValue := overCapOldPath + strings.Repeat("x", 16<<20+1-len(overCapOldPath))
	require.NoError(t, execute(database, "INSERT INTO documents (id, text_content) VALUES (?, ?)", "within-cap", "prefix "+withinCapOldPath+"/notes"))
	require.NoError(t, execute(database, "INSERT INTO documents (id, text_content) VALUES (?, ?)", "null", nil))
	require.NoError(t, execute(database, "INSERT INTO documents (id, text_content) VALUES (?, ?)", "over-cap", overCapValue))
	require.NoError(t, database.Close())

	rewriter, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rewriter.Close()) })

	transaction, err := rewriter.Begin()
	require.NoError(t, err)
	changed, err := rewriter.RewriteTextColumn(transaction, "documents", "id", "text_content", withinCapOldPath, withinCapNewPath)
	require.NoError(t, err)
	require.NoError(t, transaction.Commit())
	assert.Equal(t, 1, changed)

	transaction, err = rewriter.Begin()
	require.NoError(t, err)
	_, err = rewriter.RewriteTextColumn(transaction, "documents", "id", "text_content", overCapOldPath, overCapNewPath)
	require.Error(t, err)
	assert.ErrorContains(t, err, "documents")      //nolint:testifylint // require.Error above establishes err.
	assert.ErrorContains(t, err, "text_content")   //nolint:testifylint // require.Error above establishes err.
	assert.ErrorContains(t, err, "id=over-cap")    //nolint:testifylint // require.Error above establishes err.
	assert.ErrorContains(t, err, "16777217 bytes") //nolint:testifylint // require.Error above establishes err.
	require.NoError(t, transaction.Rollback())

	verification := openSQLite(t, path)
	var withinCapValue string
	query := "SELECT text_content FROM documents WHERE id = 'within-cap'"
	require.NoError(t, verification.QueryRowContext(context.Background(), query).Scan(&withinCapValue))
	assert.Equal(t, "prefix "+withinCapNewPath+"/notes", withinCapValue)
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
	updated, err := rewriter.UpdateColumnsByKey(transaction, "threads", "id", "present", map[string]any{"title": "new", "archived_at": 42})
	require.NoError(t, err)
	absent, err := rewriter.UpdateColumnsByKey(transaction, "threads", "id", "missing", map[string]any{"title": "never inserted", "archived_at": 42})
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

func TestPathPredicateAgreesWithByteRewriter(t *testing.T) {
	cases := []struct {
		name    string
		oldPath string
		value   string
		want    bool
	}{
		{"exact path", "/a/my_app", "/a/my_app", true},
		{"nested project", "/a/erstizeitung", "/a/erstizeitung/pwa", true},
		{"underscore nested path", "/a/my_app", "/a/my_app/notes", true},
		{"underscore wildcard nested sibling", "/a/my_app", "/a/myXapp/notes", false},
		{"underscore sibling", "/a/my_app", "/a/myXapp", false},
		{"percent nested path", "/a/100%project", "/a/100%project/notes", true},
		{"percent wildcard nested sibling", "/a/100%project", "/a/100Xproject/notes", false},
		{"percent sibling", "/a/100%project", "/a/100Xproject", false},
		{"case variant", "/a/my_app", "/a/MY_APP", false},
		{"component suffix", "/a/my_app", "/a/my_app-notes", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "parity.sqlite")
			database := openSQLite(t, path)
			require.NoError(t, execute(database, "CREATE TABLE entries (id INTEGER PRIMARY KEY, project_path TEXT)"))
			require.NoError(t, execute(database, "INSERT INTO entries (id, project_path) VALUES (1, ?)", testCase.value))
			require.NoError(t, database.Close())

			countDatabase := openSQLite(t, path)
			count, err := CountPathColumnRO(countDatabase, "entries", "project_path", testCase.oldPath)
			require.NoError(t, err)
			rewriter, err := Open(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
			transaction, err := rewriter.Begin()
			require.NoError(t, err)
			changed, err := rewriter.RewritePathColumn(transaction, "entries", "project_path", testCase.oldPath, "/a/renamed")
			require.NoError(t, err)
			require.NoError(t, transaction.Commit())

			expected, replacements := rewrite.ReplacePathInBytes([]byte(testCase.value), testCase.oldPath, "/a/renamed")
			assert.Equal(t, testCase.want, replacements == 1)
			assert.Equal(t, replacements, count)
			assert.Equal(t, replacements, changed)

			verification := openSQLite(t, path)
			var actual string
			require.NoError(t, verification.QueryRowContext(context.Background(), "SELECT project_path FROM entries WHERE id = 1").Scan(&actual))
			assert.Equal(t, string(expected), actual)
		})
	}
}

func TestPathPredicateUsesBinaryCollation(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{name: "case-variant does not match", value: "/a/MY_APP"},
		{name: "exact bytes match", value: "/a/my_app/notes"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "nocase.sqlite")
			database := openSQLite(t, path)
			require.NoError(t, execute(database, "CREATE TABLE entries (id INTEGER PRIMARY KEY, project_path TEXT COLLATE NOCASE)"))
			require.NoError(t, execute(database, "INSERT INTO entries (id, project_path) VALUES (1, ?)", testCase.value))
			require.NoError(t, database.Close())

			countDatabase := openSQLite(t, path)
			count, err := CountPathColumnRO(countDatabase, "entries", "project_path", "/a/my_app")
			require.NoError(t, err)
			rewriter, err := Open(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
			transaction, err := rewriter.Begin()
			require.NoError(t, err)
			changed, err := rewriter.RewritePathColumn(transaction, "entries", "project_path", "/a/my_app", "/a/renamed")
			require.NoError(t, err)
			require.NoError(t, transaction.Commit())

			expected, replacements := rewrite.ReplacePathInBytes([]byte(testCase.value), "/a/my_app", "/a/renamed")
			assert.Equal(t, replacements, count)
			assert.Equal(t, replacements, changed)

			verification := openSQLite(t, path)
			var actual string
			require.NoError(t, verification.QueryRowContext(context.Background(), "SELECT project_path FROM entries WHERE id = 1").Scan(&actual))
			assert.Equal(t, string(expected), actual)
		})
	}
}

func TestRewriteOperationsRejectUnexpectedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.sqlite")
	database := openSQLite(t, path)
	require.NoError(t, execute(database, "CREATE TABLE entries (identifier INTEGER PRIMARY KEY, actual_path TEXT)"))
	require.NoError(t, database.Close())

	countDatabase := openSQLite(t, path)
	_, err := CountPathColumnRO(countDatabase, "entries", "missing_path", "/a/old")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "actual_path TEXT")
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
