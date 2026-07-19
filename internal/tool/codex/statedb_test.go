package codex

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/sqlrewrite"
)

func TestCountStateDBReadOnlyUsesByteExactThreadPredicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	require.NoError(t, createStateDBNoCaseFixture(database, FixtureProjectPath()))

	planned, err := countStateDBReadOnly(database, FixtureProjectPath())
	require.NoError(t, err)

	rewriter, err := sqlrewrite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
	transaction, err := rewriter.Begin()
	require.NoError(t, err)
	applied, err := rewriteThreadsAndAgentJobs(rewriter, transaction, FixtureProjectPath(), "/Users/fixture/renamed-project")
	require.NoError(t, err)
	require.NoError(t, transaction.Commit())

	assert.Equal(t, applied, planned)
	assert.Equal(t, 1, planned)
}

func createStateDBNoCaseFixture(database *sql.DB, oldPath string) error {
	if _, err := database.ExecContext(context.Background(), `
		CREATE TABLE threads (cwd TEXT COLLATE NOCASE);
		CREATE TABLE agent_jobs (id INTEGER PRIMARY KEY, input_csv_path TEXT, output_csv_path TEXT);
	`); err != nil {
		return err
	}
	if _, err := database.ExecContext(context.Background(), `INSERT INTO threads (cwd) VALUES (?)`, oldPath); err != nil {
		return err
	}
	_, err := database.ExecContext(context.Background(), `INSERT INTO threads (cwd) VALUES (?)`, "/USERS/FIXTURE/CODEXPROJECT")
	return err
}
