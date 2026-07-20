package codex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/it-bens/cc-port/internal/sqlrewrite"
)

// Depended-on threads columns (state/migrations/0001_threads.sql,
// 0039_threads_recency_at.sql, 0040_threads_history_mode.sql): cwd,
// rollout_path, archived_at, title. Only cwd is rewritten by move; archived_at
// and title are read-only context the threads sidecar exports alongside them.
const (
	threadsTable             = "threads"
	threadsCwdColumn         = "cwd"
	threadsIDColumn          = "id"
	agentJobsTable           = "agent_jobs"
	agentJobsIDColumn        = "id"
	agentJobsInputCSVColumn  = "input_csv_path"
	agentJobsOutputCSVColumn = "output_csv_path"
)

// maxMatchedThreadRows bounds how many distinct threads.cwd values
// matchingThreadCWDs, or how many thread ids threadIDsForCWD, will
// materialize into memory before refusing, PER CALL (one database, one
// query). Real Codex homes carry at most a few hundred distinct project
// directories and thread counts in the low thousands; this ceiling leaves
// generous headroom while refusing a hostile/corrupted database that could
// otherwise exhaust memory during a dry-run count or a move (spec §5.1).
const maxMatchedThreadRows = 100_000

// maxAggregateMatchedThreadRows bounds the TOTAL number of thread ids or
// rewrites a caller accumulates across every matched cwd value and, where
// applicable, across every discovered database. The per-call cap above
// bounds one query; it does not bound the sum across many matched cwd
// values or many database files, which is what actually gets held in
// memory (matchingThreadRewrites' rewrites slice, projectThreadIDs'
// threadIDs set). Reusing the per-call cap's magnitude here keeps the
// worst-case memory for one project's whole thread footprint in the same
// generous-but-bounded range.
const maxAggregateMatchedThreadRows = 100_000

// ErrTooManyMatchedThreadRows is returned by matchingThreadCWDs and
// threadIDsForCWD when a single query has more than maxMatchedThreadRows
// matching rows, and by matchingThreadRewrites/projectThreadIDs when their
// running total across matched cwd values or databases exceeds
// maxAggregateMatchedThreadRows, refusing to materialize an unbounded
// result set rather than silently truncating it.
var ErrTooManyMatchedThreadRows = errors.New("too many matching threads rows to materialize")

// guardColumnByteCap refuses if any value in table.column exceeds
// sqlrewrite.MaxTextValueBytes, mirroring sqlrewrite.CountTextColumnRO's own
// guard-before-materialize query so a single oversized value never reaches
// Go memory even transiently, using the exact same shared cap and sentinel
// error CountTextColumnRO already returns for agent_jobs/stage1_outputs.
func guardColumnByteCap(ctx context.Context, database *sql.DB, table, column string) error {
	// #nosec G201 -- table and column names are adapter constants, never values.
	guardQuery := fmt.Sprintf("SELECT octet_length(%s) FROM %s WHERE octet_length(%s) > ? LIMIT 1", column, table, column)
	var overCapBytes int64
	switch err := database.QueryRowContext(ctx, guardQuery, sqlrewrite.MaxTextValueBytes).Scan(&overCapBytes); {
	case err == nil:
		return fmt.Errorf(
			"%w: %s.%s is %d bytes, exceeding the %d byte cap",
			sqlrewrite.ErrTextValueTooLarge, table, column, overCapBytes, sqlrewrite.MaxTextValueBytes,
		)
	case errors.Is(err, sql.ErrNoRows):
		return nil
	default:
		return fmt.Errorf("guard %s.%s: %w", table, column, err)
	}
}

// stateDBKnowsProject reports whether any discovered state_*.sqlite
// database has a thread whose cwd canonically matches oldPath (see
// matchingThreadCWDs). agent_jobs free-text columns use
// sqlrewrite.CountTextColumnRO for boundary-aware path counting. This runs
// from MoveSurfaces's project-identity preflight (move.go's projectKnown),
// which tool.Mover.MoveSurfaces receives no context for, so the scan below
// is bounded (guardColumnByteCap, maxMatchedThreadRows) but not cancellable;
// see README §cwd matching.
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

	matched, err := matchingThreadCWDs(context.Background(), database, oldPath)
	if err != nil {
		return false, fmt.Errorf("match threads.cwd: %w", err)
	}
	return len(matched) > 0, nil
}

// countStateDB reports how many occurrences a move would rewrite across
// every discovered state_*.sqlite database: threads.cwd plus any
// agent_jobs path column that references the project path. threads.cwd goes
// through the same matchingThreadCWDs computation Apply's rewrite uses, so
// the two can never disagree; the free-text agent_jobs columns are streamed
// through sqlrewrite.CountTextColumnRO so their boundary-aware path matches
// agree with Apply's rewrite logic. ctx comes from the stateDBSurfaceWithPlans
// Plan closure (move.go), so this path is both bounded and cancellable.
func countStateDB(ctx context.Context, sqliteDir, oldPath, newPath string) (int, error) {
	databases, err := discoverDatabases(sqliteDir, stateDBGlob)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, path := range databases {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		count, err := countStateDBFile(ctx, path, oldPath, newPath)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", path, err)
		}
		total += count
	}
	return total, nil
}

func countStateDBFile(ctx context.Context, path, oldPath, _ string) (int, error) {
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = database.Close() }()

	return countStateDBReadOnly(ctx, database, oldPath)
}

func countStateDBReadOnly(ctx context.Context, database *sql.DB, oldPath string) (int, error) {
	total, err := countMatchingThreadRows(ctx, database, oldPath)
	if err != nil {
		return 0, fmt.Errorf("count threads.cwd: %w", err)
	}
	for _, column := range []string{agentJobsInputCSVColumn, agentJobsOutputCSVColumn} {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		count, err := sqlrewrite.CountTextColumnRO(database, agentJobsTable, column, oldPath)
		if err != nil {
			return 0, fmt.Errorf("count agent_jobs.%s: %w", column, err)
		}
		total += count
	}
	return total, nil
}

// matchingColumnValues returns every value stored in table.column whose
// canonicalized form matches project — Codex's own
// paths_match_after_normalization comparator (spec §5.1), mirrored in Go
// because symlink resolution cannot be expressed as a SQL predicate. This
// applies only to a column holding a single verbatim cwd value per row;
// agent_jobs' and automations' free-text or multi-value columns stay on
// sqlrewrite.CountTextColumnRO's boundary-aware substring scan instead.
// DISTINCT is forced to COLLATE BINARY so a column declared with a
// case-insensitive collation cannot fold two byte-different stored values
// into one before pathMatchesProject sees them. The scan is bounded
// (guardColumnByteCap, maxMatchedThreadRows) and, when ctx is a real
// request context rather than context.Background(), cancellable per
// iteration. A NULLABLE column (automation_runs.source_cwd, unlike
// threads.cwd and local_thread_catalog.cwd, both NOT NULL) can store a NULL
// row; that row is scanned into sql.NullString and skipped as a non-match
// rather than treated as an error, matching the exclude-without-erroring
// behavior CountTextColumnRO's own instr() predicate already gives NULL
// values (instr(NULL, x) is NULL, which WHERE treats as false).
func matchingColumnValues(ctx context.Context, database *sql.DB, table, column, project string) ([]string, error) {
	if err := requireTableColumn(database, table, column); err != nil {
		return nil, err
	}
	if err := guardColumnByteCap(ctx, database, table, column); err != nil {
		return nil, err
	}
	// #nosec G201 -- table and column names are adapter constants, never values.
	query := fmt.Sprintf("SELECT DISTINCT %s COLLATE BINARY FROM %s", column, table)
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query distinct %s.%s: %w", table, column, err)
	}
	defer func() { _ = rows.Close() }()

	var matched []string
	rowCount := 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rowCount++
		if rowCount > maxMatchedThreadRows {
			return nil, fmt.Errorf(
				"%w: %s.%s has more than %d distinct values", ErrTooManyMatchedThreadRows, table, column, maxMatchedThreadRows,
			)
		}
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan %s.%s: %w", table, column, err)
		}
		if !value.Valid {
			continue
		}
		isMatch, err := pathMatchesProject(value.String, project)
		if err != nil {
			return nil, err
		}
		if isMatch {
			matched = append(matched, value.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s.%s: %w", table, column, err)
	}
	return matched, nil
}

// matchingThreadCWDs is matchingColumnValues specialized to threads.cwd.
// stateDBFileKnowsProject, countStateDBReadOnly, countThreadRows,
// projectThreadIDs, and matchingThreadRewrites all derive their row set
// from this one function, so a dry-run count and an apply can never
// disagree on which rows belong to project.
func matchingThreadCWDs(ctx context.Context, database *sql.DB, project string) ([]string, error) {
	return matchingColumnValues(ctx, database, threadsTable, threadsCwdColumn, project)
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

// countMatchingThreadRows is countMatchingColumnRows specialized to
// threads.cwd.
func countMatchingThreadRows(ctx context.Context, database *sql.DB, project string) (int, error) {
	return countMatchingColumnRows(ctx, database, threadsTable, threadsCwdColumn, project)
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
// equals storedCWD exactly. The primary key is scanned into any and
// asserted to be a Go string (SQLite TEXT storage class, the declared type
// for the real Codex schema) rather than assumed: a BLOB or other unexpected
// key type fails loudly here instead of silently mis-scanning. The scan is
// bounded the same way matchingThreadCWDs is.
func threadIDsForCWD(ctx context.Context, database *sql.DB, storedCWD string) ([]string, error) {
	if err := guardColumnByteCap(ctx, database, threadsTable, threadsIDColumn); err != nil {
		return nil, err
	}
	// #nosec G201 -- table and column names are adapter constants, never values.
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s COLLATE BINARY = ?", threadsIDColumn, threadsTable, threadsCwdColumn)
	rows, err := database.QueryContext(ctx, query, storedCWD)
	if err != nil {
		return nil, fmt.Errorf("query thread IDs for cwd %q: %w", storedCWD, err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	rowCount := 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rowCount++
		if rowCount > maxMatchedThreadRows {
			return nil, fmt.Errorf("%w: %s has more than %d thread ids for cwd %q", ErrTooManyMatchedThreadRows, threadsTable, maxMatchedThreadRows, storedCWD)
		}
		var id any
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan thread ID for cwd %q: %w", storedCWD, err)
		}
		idText, ok := id.(string)
		if !ok {
			return nil, fmt.Errorf(
				"%s.%s for cwd %q is not a TEXT primary key (got %T), refusing to rewrite by an unexpected key type",
				threadsTable, threadsIDColumn, storedCWD, id,
			)
		}
		ids = append(ids, idText)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate thread IDs for cwd %q: %w", storedCWD, err)
	}
	return ids, nil
}

// threadCWDRewrite pairs a thread's primary key with the value its
// canonically matched, verbatim-stored cwd rewrites to.
type threadCWDRewrite struct {
	id     string
	newCWD string
}

// stateDBRewritePlans records the primary-key updates selected while the
// source path still exists. Apply must use this snapshot rather than repeat
// canonicalization after another selected tool may have moved the project.
type stateDBRewritePlans map[string][]threadCWDRewrite

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
		rewrites, err := matchingThreadRewrites(ctx, path, oldPath, newPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		plans[path] = rewrites
	}
	return plans, nil
}

// matchingThreadRewrites returns, for every threads row at path whose cwd
// canonically matches oldPath, its primary key and the value Apply must
// write: newPath with whatever suffix the row's canonicalized cwd carried
// past oldPath's canonical form. This preserves the byte-exact SQL path
// predicate's old suffix-preservation semantics (newPath + substr(cwd,
// len(oldPath)+1)) for the boundary-prefix case, now computed from canonical
// rather than literal paths. It opens its own short-lived read-only
// connection to path — not the caller's write transaction, since
// sqlrewrite.Tx exposes no ad-hoc SELECT —
// and closes it before returning, so the write transaction never overlaps a
// read past this point. Its sole caller, stateDBRewritePlansForProject, runs
// during MoveSurfaces' preflight (captureMovePreflight, move.go) with
// context.Background() — MoveSurfaces takes no context of its own — so the
// canonicalization always happens while oldPath still exists, before any
// selected tool's apply could have removed it; it is bounded but not
// cancellable from a caller's context.
// threadIDsForCWD already bounds each matched cwd's own id count; this loop
// additionally bounds the running TOTAL across every matched cwd, since up
// to maxMatchedThreadRows matched values could each independently
// contribute up to maxMatchedThreadRows ids.
func matchingThreadRewrites(ctx context.Context, path, oldPath, newPath string) ([]threadCWDRewrite, error) {
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()

	matchedCWDs, err := matchingThreadCWDs(ctx, database, oldPath)
	if err != nil {
		return nil, fmt.Errorf("match %s.%s: %w", threadsTable, threadsCwdColumn, err)
	}
	canonicalOldPath, err := canonicalizePath(oldPath)
	if err != nil {
		return nil, err
	}

	var rewrites []threadCWDRewrite
	for _, storedCWD := range matchedCWDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		canonicalStoredCWD, err := canonicalizePath(storedCWD)
		if err != nil {
			return nil, err
		}
		suffix := strings.TrimPrefix(canonicalStoredCWD, canonicalOldPath)
		newCWD := newPath + suffix
		ids, err := threadIDsForCWD(ctx, database, storedCWD)
		if err != nil {
			return nil, fmt.Errorf("thread IDs for cwd %q: %w", storedCWD, err)
		}
		for _, id := range ids {
			if len(rewrites) >= maxAggregateMatchedThreadRows {
				return nil, fmt.Errorf(
					"%w: more than %d rewrites accumulated across matched cwd values in %s",
					ErrTooManyMatchedThreadRows, maxAggregateMatchedThreadRows, path,
				)
			}
			rewrites = append(rewrites, threadCWDRewrite{id: id, newCWD: newCWD})
		}
	}
	return rewrites, nil
}

// rewriteThreadsAndAgentJobsWithPlan rewrites threads.cwd for every row in
// rewrites — a canonical-match plan stateDBRewritePlansForProject captured
// during preflight, before any selected tool's apply could have removed
// oldPath from disk (spec §5.1) — by primary key through UpdateColumnsByKey,
// plus agent_jobs' free-text path columns.
func rewriteThreadsAndAgentJobsWithPlan(
	ctx context.Context, database *sqlrewrite.DB, transaction *sqlrewrite.Tx, rewrites []threadCWDRewrite, oldPath, newPath string,
) (int, error) {
	count := 0
	for _, threadRewrite := range rewrites {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		values := map[string]any{threadsCwdColumn: threadRewrite.newCWD}
		updated, err := database.UpdateColumnsByKey(transaction, threadsTable, threadsIDColumn, threadRewrite.id, values)
		if err != nil {
			return 0, fmt.Errorf("rewrite %s.%s for id %s: %w", threadsTable, threadsCwdColumn, threadRewrite.id, err)
		}
		count += updated
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
