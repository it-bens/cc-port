package codex

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/it-bens/cc-port/internal/sqlrewrite"
)

// Depended-on threads columns, all declared in state/migrations/0001_threads.sql:
// id, cwd, archived_at, title, git_sha, git_branch, git_origin_url. Move
// matches and rewrites cwd, addressing rows by id. Export reads id and the
// other five into the threads sidecar, and import writes those five back to
// existing rows by id.
const (
	threadsTable     = "threads"
	threadsCwdColumn = "cwd"
	threadsIDColumn  = "id"
)

// Depended-on project_roots column (state/migrations/0049_projects.sql):
// path, one absolute project root per row. The table's declared primary key
// is (project_id, position), so move addresses its rows by rowid.
const (
	projectRootsTable      = "project_roots"
	projectRootsPathColumn = "path"
)

// stateDBRowKeyKind names how move addresses a matched state-db row.
type stateDBRowKeyKind int

const (
	// textPrimaryKey addresses a row by its declared single-column TEXT
	// primary key.
	textPrimaryKey stateDBRowKeyKind = iota
	// rowIDKey addresses a row by its SQLite rowid.
	rowIDKey
)

// stateDBPathColumn is one state-db column holding a single verbatim project
// path per row, plus how move addresses the rows it rewrites. rowKind names
// the rows in error messages.
type stateDBPathColumn struct {
	table     string
	column    string
	keyColumn string
	keyKind   stateDBRowKeyKind
	rowKind   string
}

var (
	threadsCWDPath = stateDBPathColumn{
		table: threadsTable, column: threadsCwdColumn, keyColumn: threadsIDColumn, keyKind: textPrimaryKey, rowKind: "thread",
	}
	projectRootsPath = stateDBPathColumn{
		table: projectRootsTable, column: projectRootsPathColumn, keyColumn: "rowid", keyKind: rowIDKey, rowKind: "project root",
	}

	// stateDBPathColumns lists every state-db column move rewrites. Plan
	// capture ranges over this list; Plan counts and Apply writes the
	// captured plan.
	stateDBPathColumns = []stateDBPathColumn{threadsCWDPath, projectRootsPath}
)

// stateDBKnowsProject reports whether any discovered state_*.sqlite
// database has a row in any stateDBPathColumns column whose value
// canonically matches project: a thread's cwd or a project's root. It first
// requires every stateDBPathColumns column in every discovered database, so
// a database missing one is a schema error whether or not another database
// already knows the project. It is the state-database identity check for
// both move (projectKnown) and stats/export (knowsProject). MoveSurfaces
// receives no context, so the move path passes context.Background() and is
// not cancellable.
func stateDBKnowsProject(ctx context.Context, sqliteDir, project string) (bool, error) {
	databases, err := discoverDatabases(sqliteDir, stateDBGlob)
	if err != nil {
		return false, err
	}
	for _, path := range databases {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := requireStateDBPathColumns(path); err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
	}
	for _, path := range databases {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		known, err := stateDBFileKnowsProject(ctx, path, project)
		if err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
		if known {
			return true, nil
		}
	}
	return false, nil
}

// requireStateDBPathColumns fails when the database at path lacks any
// stateDBPathColumns table or column, or when a table's key shape does not
// match what Apply's keyed update will require: a single-column primary key
// for a textPrimaryKey target, an ordinary rowid table for a rowIDKey
// target. Running the same checks Apply runs, for every discovered database
// regardless of whether any row matches, makes a dry run fail on the same
// schema error Apply would give (README §cwd matching).
func requireStateDBPathColumns(path string) error {
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	for _, target := range stateDBPathColumns {
		if err := target.requireSchema(database); err != nil {
			return err
		}
	}
	return nil
}

// requireSchema checks the schema shape target.update will require at apply
// time.
func (target stateDBPathColumn) requireSchema(database *sql.DB) error {
	switch target.keyKind {
	case textPrimaryKey:
		return sqlrewrite.RequirePrimaryKeyAndColumns(database, target.table, target.keyColumn, target.column)
	case rowIDKey:
		return sqlrewrite.RequireRowIDTableAndColumns(database, target.table, target.column)
	default:
		return fmt.Errorf("unknown row key kind %d", target.keyKind)
	}
}

func stateDBFileKnowsProject(ctx context.Context, path, project string) (bool, error) {
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = database.Close() }()

	for _, target := range stateDBPathColumns {
		matched, err := matchingColumnValues(ctx, database, target.table, target.column, project)
		if err != nil {
			return false, err
		}
		if len(matched) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// stateDBProjectPaths returns the distinct project paths stored in every
// stateDBPathColumns column across every discovered state_*.sqlite
// database, one value per canonical path: of the byte-different values that
// canonicalize to the same path, the lexically smallest.
func stateDBProjectPaths(ctx context.Context, sqliteDir string) ([]string, error) {
	databases, err := discoverDatabases(sqliteDir, stateDBGlob)
	if err != nil {
		return nil, err
	}
	byCanonical := make(map[string]string)
	for _, path := range databases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := collectStateDBProjectPaths(ctx, path, byCanonical); err != nil {
			return nil, fmt.Errorf("read project directories from database %s: %w", path, err)
		}
	}
	paths := make([]string, 0, len(byCanonical))
	for _, value := range byCanonical {
		paths = append(paths, value)
	}
	sort.Strings(paths)
	return paths, nil
}

func collectStateDBProjectPaths(ctx context.Context, path string, byCanonical map[string]string) error {
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	for _, target := range stateDBPathColumns {
		values, err := distinctColumnValues(ctx, database, target.table, target.column)
		if err != nil {
			return fmt.Errorf("read %s.%s: %w", target.table, target.column, err)
		}
		for _, value := range values {
			canonical, err := canonicalizePath(value)
			if err != nil {
				return err
			}
			if current, seen := byCanonical[canonical]; !seen || value < current {
				byCanonical[canonical] = value
			}
		}
	}
	return nil
}

// matchingColumnValues returns every value stored in table.column whose
// canonicalized form matches project — Codex's own
// paths_match_after_normalization comparator (spec §5.1), mirrored in Go
// because symlink resolution cannot be expressed as a SQL predicate. This
// applies only to a column holding a single verbatim cwd value per row;
// automations.cwds, a multi-value column, stays on
// sqlrewrite.CountTextColumnRO's boundary-aware substring scan instead.
// DISTINCT is forced to COLLATE BINARY so a column declared with a
// case-insensitive collation cannot fold two byte-different stored values
// into one before pathMatchesProject sees them. When ctx is a real
// request context rather than context.Background(), cancellable per
// iteration. A NULLABLE column (automation_runs.source_cwd, unlike
// threads.cwd and local_thread_catalog.cwd, both NOT NULL) can store a NULL
// row; that row is scanned into sql.NullString and skipped as a non-match
// rather than treated as an error, matching the exclude-without-erroring
// behavior CountTextColumnRO's own instr() predicate already gives NULL
// values (instr(NULL, x) is NULL, which WHERE treats as false).
func matchingColumnValues(ctx context.Context, database *sql.DB, table, column, project string) ([]string, error) {
	values, err := distinctColumnValues(ctx, database, table, column)
	if err != nil {
		return nil, err
	}
	var matched []string
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		isMatch, err := pathMatchesProject(value, project)
		if err != nil {
			return nil, err
		}
		if isMatch {
			matched = append(matched, value)
		}
	}
	return matched, nil
}

// distinctColumnValues returns every distinct non-NULL value in
// table.column, compared COLLATE BINARY, after requiring the column.
func distinctColumnValues(ctx context.Context, database *sql.DB, table, column string) ([]string, error) {
	if err := requireTableColumn(database, table, column); err != nil {
		return nil, err
	}
	// #nosec G201 -- table and column names are adapter constants, never values.
	query := fmt.Sprintf("SELECT DISTINCT %s COLLATE BINARY FROM %s", column, table)
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query distinct %s.%s: %w", table, column, err)
	}
	defer func() { _ = rows.Close() }()

	var values []string
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan %s.%s: %w", table, column, err)
		}
		if value.Valid {
			values = append(values, value.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s.%s: %w", table, column, err)
	}
	return values, nil
}

// countRowsForColumnValue reports how many table rows carry storedValue in
// column exactly (a byte-exact value matchingColumnValues reported),
// COLLATE BINARY so the count cannot widen past what matchingColumnValues
// already decided.
func countRowsForColumnValue(ctx context.Context, database *sql.DB, table, column, storedValue string) (int, error) {
	// #nosec G201 -- table and column names are adapter constants, never values.
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s COLLATE BINARY = ?", table, column)
	var count int
	if err := database.QueryRowContext(ctx, query, storedValue).Scan(&count); err != nil {
		return 0, fmt.Errorf("count %s for value %q: %w", table, storedValue, err)
	}
	return count, nil
}

// countMatchingColumnRows sums table rows across every value
// matchingColumnValues reports as belonging to project. codexDevWarning
// reuses this directly for codex-dev.db's local_thread_catalog.cwd and
// automation_runs.source_cwd, so a symlink-aliased value in either is
// detected the same way a symlink-aliased threads.cwd now is (spec §5.1).
func countMatchingColumnRows(ctx context.Context, database *sql.DB, table, column, project string) (int, error) {
	matched, err := matchingColumnValues(ctx, database, table, column, project)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, value := range matched {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		count, err := countRowsForColumnValue(ctx, database, table, column, value)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

// countMatchingColumnRowsBackground adapts countMatchingColumnRows to
// sqlrewrite.CountTextColumnRO's signature shape so codexDevWarning
// (move.go) can select either counting strategy per column in one
// table-driven loop. codexDevWarning has no context of its own (move.go's
// MoveSurfaces and ResidualWarnings, its only callers, take none), so this
// always scans with context.Background(): bounded the same way every other
// canonical match is, not cancellable.
func countMatchingColumnRowsBackground(database *sql.DB, table, column, project string) (int, error) {
	return countMatchingColumnRows(context.Background(), database, table, column, project)
}

// threadIDsForCWD returns the primary keys of every threads row whose cwd
// equals storedCWD exactly.
func threadIDsForCWD(ctx context.Context, database *sql.DB, storedCWD string) ([]string, error) {
	keys, err := rowKeysForColumnValue(ctx, database, threadsCWDPath, storedCWD)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(keys))
	for _, key := range keys {
		id, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("%s.%s is %T, not string", threadsTable, threadsIDColumn, key)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// rowKeysForColumnValue returns the key of every target row whose column
// equals storedValue exactly. Each key is scanned into any and asserted to
// be the Go type target.keyKind implies (string for a TEXT primary key,
// int64 for a rowid) rather than assumed: an unexpected storage class fails
// loudly here instead of silently mis-scanning.
func rowKeysForColumnValue(ctx context.Context, database *sql.DB, target stateDBPathColumn, storedValue string) ([]any, error) {
	// #nosec G201 -- table and column names are adapter constants, never values.
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s COLLATE BINARY = ?", target.keyColumn, target.table, target.column)
	rows, err := database.QueryContext(ctx, query, storedValue)
	if err != nil {
		return nil, fmt.Errorf("query %s.%s for %s %q: %w", target.table, target.keyColumn, target.column, storedValue, err)
	}
	defer func() { _ = rows.Close() }()

	var keys []any
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var key any
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan %s.%s for %s %q: %w", target.table, target.keyColumn, target.column, storedValue, err)
		}
		if err := target.checkKey(key); err != nil {
			return nil, fmt.Errorf("%s.%s for %s %q: %w", target.table, target.keyColumn, target.column, storedValue, err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s.%s for %s %q: %w", target.table, target.keyColumn, target.column, storedValue, err)
	}
	return keys, nil
}

func (target stateDBPathColumn) checkKey(key any) error {
	switch target.keyKind {
	case textPrimaryKey:
		if _, ok := key.(string); !ok {
			return fmt.Errorf("not a TEXT primary key (got %T), refusing to rewrite by an unexpected key type", key)
		}
	case rowIDKey:
		if _, ok := key.(int64); !ok {
			return fmt.Errorf("not an INTEGER rowid (got %T), refusing to rewrite by an unexpected key type", key)
		}
	default:
		return fmt.Errorf("unknown row key kind %d", target.keyKind)
	}
	return nil
}

// update writes newValue to target.column on the row key addresses, through
// the sqlrewrite primitive matching target.keyKind, and only while the row
// still holds oldValue, the value the plan matched.
func (target stateDBPathColumn) update(
	database *sqlrewrite.DB, transaction *sqlrewrite.Tx, key any, oldValue, newValue string,
) (int, error) {
	values := map[string]any{target.column: newValue}
	expected := map[string]any{target.column: oldValue}
	switch target.keyKind {
	case textPrimaryKey:
		return database.UpdateColumnsByKey(transaction, target.table, target.keyColumn, key, values, expected)
	case rowIDKey:
		rowID, ok := key.(int64)
		if !ok {
			return 0, fmt.Errorf("%s rowid is %T, not int64", target.table, key)
		}
		return database.UpdateColumnsByRowID(transaction, target.table, rowID, values, expected)
	default:
		return 0, fmt.Errorf("unknown row key kind %d", target.keyKind)
	}
}

// stateDBPathRewrite pairs one matched state-db row with the value its
// canonically matched, verbatim-stored path rewrites to.
type stateDBPathRewrite struct {
	target   stateDBPathColumn
	key      any
	oldValue string
	newValue string
}

// stateDBRewritePlans records the keyed updates selected while the source
// path still exists. Apply must use this snapshot rather than repeat
// canonicalization after another selected tool may have moved the project.
type stateDBRewritePlans map[string][]stateDBPathRewrite

// rowCount is the state-db surface's Plan count: every row the captured
// plan will write. Apply writes each planned row or fails, so a successful
// Apply reports the same count.
func (plans stateDBRewritePlans) rowCount() int {
	total := 0
	for _, rewrites := range plans {
		total += len(rewrites)
	}
	return total
}

// requirePlannedDatabases fails unless discovered names exactly the
// databases the plan was captured from. A database added since the plan was
// never matched, and one removed would leave its planned rows unwritten.
func (plans stateDBRewritePlans) requirePlannedDatabases(discovered []string) error {
	current := make(map[string]struct{}, len(discovered))
	var added []string
	for _, path := range discovered {
		current[path] = struct{}{}
		if _, planned := plans[path]; !planned {
			added = append(added, path)
		}
	}
	var removed []string
	for path := range plans {
		if _, present := current[path]; !present {
			removed = append(removed, path)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}
	sort.Strings(removed)
	return fmt.Errorf("state databases changed after the plan: added %v, removed %v", added, removed)
}

func stateDBRewritePlansForProject(ctx context.Context, sqliteDir, oldPath, newPath string) (stateDBRewritePlans, error) {
	paths, err := discoverDatabases(sqliteDir, stateDBGlob)
	if err != nil {
		return nil, err
	}
	plans := make(stateDBRewritePlans, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rewrites, err := matchingPathRewrites(ctx, path, oldPath, newPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		plans[path] = rewrites
	}
	return plans, nil
}

// matchingPathRewrites returns, for every row at path whose
// stateDBPathColumns column canonically matches oldPath, its key and the
// value Apply must write: newPath with whatever suffix the row's
// canonicalized value carried past oldPath's canonical form. This preserves
// the byte-exact SQL path predicate's suffix-preservation semantics (newPath
// + substr(value, len(oldPath)+1)) for the boundary-prefix case, computed
// from canonical rather than literal paths. It opens its own short-lived
// read-only connection to path — not the caller's write transaction, since
// sqlrewrite.Tx exposes no ad-hoc SELECT — and closes it before returning,
// so the write transaction never overlaps a read past this point. Its sole
// caller, stateDBRewritePlansForProject, runs during MoveSurfaces' preflight
// (captureMovePreflight, move.go) with context.Background() — MoveSurfaces
// takes no context of its own — so the canonicalization always happens while
// oldPath still exists, before any selected tool's apply could have removed
// it; it is not cancellable from a caller's context.
func matchingPathRewrites(ctx context.Context, path, oldPath, newPath string) ([]stateDBPathRewrite, error) {
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()

	canonicalOldPath, err := canonicalizePath(oldPath)
	if err != nil {
		return nil, err
	}
	var rewrites []stateDBPathRewrite
	for _, target := range stateDBPathColumns {
		matchedValues, err := matchingColumnValues(ctx, database, target.table, target.column, oldPath)
		if err != nil {
			return nil, err
		}
		for _, storedValue := range matchedValues {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			canonicalStoredValue, err := canonicalizePath(storedValue)
			if err != nil {
				return nil, err
			}
			newValue := newPath + strings.TrimPrefix(canonicalStoredValue, canonicalOldPath)
			keys, err := rowKeysForColumnValue(ctx, database, target, storedValue)
			if err != nil {
				return nil, err
			}
			for _, key := range keys {
				rewrites = append(rewrites, stateDBPathRewrite{target: target, key: key, oldValue: storedValue, newValue: newValue})
			}
		}
	}
	return rewrites, nil
}

// rewriteStateDBPathsWithPlan writes every row in rewrites — a
// canonical-match plan stateDBRewritePlansForProject captured during
// preflight, before any selected tool's apply could have removed oldPath from
// disk (spec §5.1) — by the key the plan recorded. A planned row that updates
// nothing fails the apply: the state database changed after the plan.
func rewriteStateDBPathsWithPlan(
	ctx context.Context, database *sqlrewrite.DB, transaction *sqlrewrite.Tx, rewrites []stateDBPathRewrite,
) (int, error) {
	count := 0
	for _, pathRewrite := range rewrites {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		target := pathRewrite.target
		updated, err := target.update(database, transaction, pathRewrite.key, pathRewrite.oldValue, pathRewrite.newValue)
		if err != nil {
			return 0, fmt.Errorf("rewrite %s.%s for %s %v: %w", target.table, target.column, target.keyColumn, pathRewrite.key, err)
		}
		if updated == 0 {
			return 0, fmt.Errorf(
				"rewrite %s.%s for %s %v: state database changed after the plan: no row with that key holds %q any more",
				target.table, target.column, target.keyColumn, pathRewrite.key, pathRewrite.oldValue,
			)
		}
		count += updated
	}
	return count, nil
}
