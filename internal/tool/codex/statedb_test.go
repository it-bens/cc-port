package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/sqlrewrite"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/codex/codexschema"
)

func TestMove_RewritesProjectRootsPathAlongsideThreadCwd(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}

	planCounts, applyCounts := planAndApply(t, workspace, req)

	assert.Equal(t, 2, planCounts["state-db"], "one threads row and one project_roots row reference the fixture project")
	assert.Equal(t, planCounts["state-db"], applyCounts["state-db"])
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, codexschema.StateDBFileName))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	var rootPath string
	require.NoError(t, database.QueryRowContext(context.Background(),
		`SELECT path FROM project_roots WHERE project_id = ?`, fixtureProjectID).Scan(&rootPath))
	assert.Equal(t, req.NewPath, rootPath)
}

func TestStateDBKnowsProjectReferencedOnlyByProjectRoot(t *testing.T) {
	home := SetupFixture(t)
	const rootOnlyProject = "/Users/test/Projects/root-only"
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, codexschema.StateDBFileName))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.ExecContext(context.Background(),
		`INSERT INTO project_roots (project_id, position, path) VALUES (?, ?, ?)`, fixtureProjectID, 1, rootOnlyProject)
	require.NoError(t, err)

	known, err := stateDBKnowsProject(context.Background(), home.SQLiteDir, rootOnlyProject)

	require.NoError(t, err)
	assert.True(t, known)
}

func TestMove_StateDBApplyFailsWhenProjectRootChangedAfterPlan(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	surfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	require.Equal(t, "state-db", surfaces[0].Name)
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, codexschema.StateDBFileName))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.ExecContext(context.Background(),
		`UPDATE project_roots SET path = ? WHERE project_id = ?`, "/Users/test/Projects/edited-root", fixtureProjectID)
	require.NoError(t, err)
	undo := tool.NewRestorer()

	_, applyErr := surfaces[0].Apply(context.Background(), undo)
	restoreErr := undo.Restore()

	require.ErrorContains(t, applyErr,
		`rewrite project_roots.path for rowid 1: state database changed after the plan: no row with that key holds "`+FixtureProjectPath()+`" any more`)
	require.NoError(t, restoreErr)
	var threadCWD string
	require.NoError(t, database.QueryRowContext(context.Background(), `SELECT cwd FROM threads LIMIT 1`).Scan(&threadCWD))
	assert.Equal(t, FixtureProjectPath(), threadCWD, "undo rolls back the threads row the same transaction already rewrote")
}

func TestMove_StateDBApplyFailsWhenThreadCwdChangedAfterPlan(t *testing.T) {
	workspace, home := fixtureWorkspace(t)
	req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
	surfaces, err := workspace.MoveSurfaces(req)
	require.NoError(t, err)
	require.Equal(t, "state-db", surfaces[0].Name)
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, codexschema.StateDBFileName))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.ExecContext(context.Background(),
		`UPDATE threads SET cwd = ? WHERE id = ?`, "/Users/test/Projects/edited-cwd", fixtureThreadOne)
	require.NoError(t, err)
	undo := tool.NewRestorer()

	_, applyErr := surfaces[0].Apply(context.Background(), undo)
	restoreErr := undo.Restore()

	require.ErrorContains(t, applyErr,
		`rewrite threads.cwd for id `+fixtureThreadOne+`: state database changed after the plan: no row with that key holds "`+
			FixtureProjectPath()+`" any more`)
	require.NoError(t, restoreErr)
	var threadCWD string
	require.NoError(t, database.QueryRowContext(context.Background(),
		`SELECT cwd FROM threads WHERE id = ?`, fixtureThreadOne).Scan(&threadCWD))
	assert.Equal(t, "/Users/test/Projects/edited-cwd", threadCWD, "the drifted value stays as Codex wrote it")
}

func TestMove_StateDBApplyFailsWhenStateDatabasesChangedAfterPlan(t *testing.T) {
	cases := []struct {
		name    string
		change  func(t *testing.T, home *Home)
		wantErr func(home *Home) string
	}{
		{
			name: "database added",
			change: func(t *testing.T, home *Home) {
				t.Helper()
				buildFixtureStateDB(t, filepath.Join(home.SQLiteDir, "state_6.sqlite"))
			},
			wantErr: func(home *Home) string {
				return "state databases changed after the plan: added [" + filepath.Join(home.SQLiteDir, "state_6.sqlite") + "], removed []"
			},
		},
		{
			name: "database removed",
			change: func(t *testing.T, home *Home) {
				t.Helper()
				require.NoError(t, os.Remove(filepath.Join(home.SQLiteDir, codexschema.StateDBFileName)))
			},
			wantErr: func(home *Home) string {
				return "state databases changed after the plan: added [], removed [" + filepath.Join(home.SQLiteDir, codexschema.StateDBFileName) + "]"
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			workspace, home := fixtureWorkspace(t)
			req := tool.MoveRequest{OldPath: FixtureProjectPath(), NewPath: "/Users/fixture/renamed-project"}
			surfaces, err := workspace.MoveSurfaces(req)
			require.NoError(t, err)
			require.Equal(t, "state-db", surfaces[0].Name)
			testCase.change(t, home)

			_, err = surfaces[0].Apply(context.Background(), tool.NewRestorer())

			require.EqualError(t, err, testCase.wantErr(home))
		})
	}
}

func TestStateDBIdentityFailsForPre0049DatabaseEvenWhenAThreadMatches(t *testing.T) {
	home := SetupFixture(t)
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, codexschema.StateDBFileName))
	require.NoError(t, err)
	_, err = database.ExecContext(context.Background(), `DROP TABLE project_roots`)
	require.NoError(t, err)
	require.NoError(t, database.Close())

	_, err = stateDBKnowsProject(context.Background(), home.SQLiteDir, FixtureProjectPath())

	require.EqualError(t, err, filepath.Join(home.SQLiteDir, codexschema.StateDBFileName)+
		`: unexpected schema for table "project_roots": table is missing; observed no columns`)
}

// TestStateDBKnowsProjectFailsWhenProjectRootsIsWithoutRowID guards state
// preflight schema parity: requireStateDBPathColumns must run the same
// rowid-table check UpdateColumnsByRowID runs at Apply, so a dry run (which
// only ever reaches moveIdentity → stateDBKnowsProject) refuses a WITHOUT
// ROWID project_roots table with the identical error Apply would give,
// rather than reporting a plan and failing only once a row is actually
// written. The recreated table holds no row for any project, and the
// project queried matches nothing in the database either: the schema check
// must still fire, since requireStateDBPathColumns runs for every discovered
// database before any project match, independent of whether a rewrite would
// ever be planned.
func TestStateDBKnowsProjectFailsWhenProjectRootsIsWithoutRowID(t *testing.T) {
	home := SetupFixture(t)
	const unrelatedProject = "/Users/test/Projects/unrelated"
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, codexschema.StateDBFileName))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.ExecContext(context.Background(), `
		DROP TABLE project_roots;
		CREATE TABLE project_roots (
			project_id TEXT NOT NULL, position INTEGER NOT NULL, path TEXT NOT NULL,
			PRIMARY KEY (project_id, position)
		) WITHOUT ROWID;
	`)
	require.NoError(t, err)

	_, err = stateDBKnowsProject(context.Background(), home.SQLiteDir, unrelatedProject)

	require.EqualError(t, err, filepath.Join(home.SQLiteDir, codexschema.StateDBFileName)+
		`: unexpected schema for table "project_roots": an ordinary rowid table is required; observed kinds [table], without rowid true`)
}

// TestStateDBKnowsProjectFailsWhenThreadsHasACompositePrimaryKey guards the
// threads-side half of the same parity: requireStateDBPathColumns must
// refuse a threads table whose primary key is composite, the same schema
// UpdateColumnsByKey would refuse at Apply, before any row is written. As in
// the project_roots case above, the recreated table holds no row and the
// queried project matches nothing, so the failure demonstrates the check
// fires independent of any planned rewrite.
func TestStateDBKnowsProjectFailsWhenThreadsHasACompositePrimaryKey(t *testing.T) {
	home := SetupFixture(t)
	const unrelatedProject = "/Users/test/Projects/unrelated"
	database, err := sql.Open("sqlite", filepath.Join(home.SQLiteDir, codexschema.StateDBFileName))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.ExecContext(context.Background(), `
		DROP TABLE threads;
		CREATE TABLE threads (id TEXT NOT NULL, cwd TEXT NOT NULL, source TEXT NOT NULL, PRIMARY KEY (id, source));
	`)
	require.NoError(t, err)

	_, err = stateDBKnowsProject(context.Background(), home.SQLiteDir, unrelatedProject)

	require.ErrorContains(t, err, `unexpected schema for table "threads": composite primary keys are unsupported`)
}

func TestStateDBPlanMatchesByteExactValuesUnderCaseInsensitiveCollation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	require.NoError(t, createStateDBNoCaseFixture(database, FixtureProjectPath()))
	const newPath = "/Users/fixture/renamed-project"

	rewrites, err := matchingPathRewrites(context.Background(), path, FixtureProjectPath(), newPath)
	require.NoError(t, err)
	rewriter, err := sqlrewrite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rewriter.Close()) })
	transaction, err := rewriter.Begin()
	require.NoError(t, err)
	applied, err := rewriteStateDBPathsWithPlan(context.Background(), rewriter, transaction, rewrites)
	require.NoError(t, err)
	require.NoError(t, transaction.Commit())

	assert.Len(t, rewrites, 2, "only the exact-case threads row and the exact-case project_roots row match")
	assert.Equal(t, len(rewrites), applied)
	assert.Equal(t, []string{newPath, noCaseAliasPath}, columnValuesInKeyOrder(t, database, "SELECT cwd FROM threads ORDER BY id"))
	assert.Equal(t, []string{newPath, noCaseAliasPath}, columnValuesInKeyOrder(t, database, "SELECT path FROM project_roots ORDER BY position"))
}

func TestStateDBPlanFailsForMissingProjectRootsPathColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.ExecContext(context.Background(), `
		CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT NOT NULL);
		CREATE TABLE project_roots (project_id TEXT NOT NULL, position INTEGER NOT NULL, PRIMARY KEY (project_id, position));
	`)
	require.NoError(t, err)

	_, err = matchingPathRewrites(context.Background(), path, FixtureProjectPath(), "/Users/fixture/renamed-project")

	require.EqualError(t, err, "required column project_roots.path is missing (observed columns: project_id, position)")
}

func TestStateDBPlanFailsForMissingProjectRootsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	_, err = database.ExecContext(context.Background(), `CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT NOT NULL);`)
	require.NoError(t, err)

	_, err = matchingPathRewrites(context.Background(), path, FixtureProjectPath(), "/Users/fixture/renamed-project")

	require.EqualError(t, err, "required column project_roots.path is missing (observed schema: table absent)")
}

// noCaseAliasPath is FixtureProjectPath upper-cased: byte-different, but
// equal under COLLATE NOCASE.
const noCaseAliasPath = "/USERS/FIXTURE/CODEXPROJECT"

// createStateDBNoCaseFixture declares threads.cwd and project_roots.path
// COLLATE NOCASE (the production schema declares no explicit collation,
// defaulting to BINARY; this fixture exercises the adversarial case) and
// inserts, in each table, an exact-case row alongside a byte-different,
// collation-equal upper-case row, so a predicate that trusted the column's
// declared collation instead of forcing COLLATE BINARY would wrongly count or
// rewrite both. threads declares its id primary key and project_roots its
// composite (project_id, position) key, matching the real schema
// (buildFixtureStateDB): move rewrites threads by id and project_roots by
// rowid.
func createStateDBNoCaseFixture(database *sql.DB, oldPath string) error {
	_, err := database.ExecContext(context.Background(), `
		CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT COLLATE NOCASE);
		CREATE TABLE project_roots (
			project_id TEXT NOT NULL, position INTEGER NOT NULL, path TEXT NOT NULL COLLATE NOCASE,
			PRIMARY KEY (project_id, position)
		);
	`)
	if err != nil {
		return err
	}
	rows := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO threads (id, cwd) VALUES (?, ?)`, []any{"exact-case-thread", oldPath}},
		{`INSERT INTO threads (id, cwd) VALUES (?, ?)`, []any{"upper-case-thread", noCaseAliasPath}},
		{`INSERT INTO project_roots (project_id, position, path) VALUES (?, ?, ?)`, []any{"primary-project", 0, oldPath}},
		{`INSERT INTO project_roots (project_id, position, path) VALUES (?, ?, ?)`, []any{"primary-project", 1, noCaseAliasPath}},
	}
	for _, row := range rows {
		if _, err := database.ExecContext(context.Background(), row.query, row.args...); err != nil {
			return err
		}
	}
	return nil
}

func columnValuesInKeyOrder(t *testing.T, database *sql.DB, query string) []string {
	t.Helper()
	rows, err := database.QueryContext(context.Background(), query)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	var values []string
	for rows.Next() {
		var value string
		require.NoError(t, rows.Scan(&value))
		values = append(values, value)
	}
	require.NoError(t, rows.Err())
	return values
}
