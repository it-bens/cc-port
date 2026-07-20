package codex

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
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

	planned, err := countStateDBReadOnly(context.Background(), database, FixtureProjectPath())
	require.NoError(t, err)

	rewriter, err := sqlrewrite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
	transaction, err := rewriter.Begin()
	require.NoError(t, err)
	applied, err := rewriteThreadsAndAgentJobs(context.Background(), path, rewriter, transaction, FixtureProjectPath(), "/Users/fixture/renamed-project")
	require.NoError(t, err)
	require.NoError(t, transaction.Commit())

	assert.Equal(t, applied, planned)
	assert.Equal(t, 1, planned)
}

// TestMatchingThreadCWDsRefusesOversizedCWD guards the bound in
// guardColumnByteCap: a threads.cwd value larger than
// sqlrewrite.MaxTextValueBytes must refuse before matchingThreadCWDs
// materializes it, with the same discriminable sqlrewrite.ErrTextValueTooLarge
// sentinel CountTextColumnRO already returns for agent_jobs/stage1_outputs,
// rather than silently scanning an unbounded value into memory.
func TestMatchingThreadCWDsRefusesOversizedCWD(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.ExecContext(context.Background(), `CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT NOT NULL)`)
	require.NoError(t, err)
	oversizedCWD := "/" + strings.Repeat("a", sqlrewrite.MaxTextValueBytes+1)
	_, err = database.ExecContext(context.Background(), `INSERT INTO threads (id, cwd) VALUES (?, ?)`, "oversized-thread", oversizedCWD)
	require.NoError(t, err)

	_, err = matchingThreadCWDs(context.Background(), database, FixtureProjectPath())

	require.ErrorIs(t, err, sqlrewrite.ErrTextValueTooLarge)
}

// createStateDBNoCaseFixture declares threads.cwd COLLATE NOCASE (the
// production schema declares no explicit collation, defaulting to BINARY;
// this fixture exercises the adversarial case) and inserts an exact-case
// row alongside a byte-different, collation-equal upper-case row, so a
// predicate that trusted the column's declared collation instead of forcing
// COLLATE BINARY would wrongly count or rewrite both. id is a declared
// primary key because rewriteThreadsAndAgentJobs now rewrites matched rows
// by primary key (spec §5.1), matching the real threads schema
// (buildFixtureStateDB), which always declares one.
func createStateDBNoCaseFixture(database *sql.DB, oldPath string) error {
	if _, err := database.ExecContext(context.Background(), `
		CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT COLLATE NOCASE);
		CREATE TABLE agent_jobs (id INTEGER PRIMARY KEY, input_csv_path TEXT, output_csv_path TEXT);
	`); err != nil {
		return err
	}
	if _, err := database.ExecContext(context.Background(), `INSERT INTO threads (id, cwd) VALUES (?, ?)`, "exact-case-thread", oldPath); err != nil {
		return err
	}
	_, err := database.ExecContext(
		context.Background(), `INSERT INTO threads (id, cwd) VALUES (?, ?)`, "upper-case-thread", "/USERS/FIXTURE/CODEXPROJECT",
	)
	return err
}
