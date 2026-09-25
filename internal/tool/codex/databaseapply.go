package codex

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/it-bens/cc-port/internal/sqlrewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

// databaseRewrite holds one intentionally uncommitted database transaction.
// File surfaces run while these transactions are open; the final surface is
// the only place they commit.
type databaseRewrite struct {
	path        string
	database    *sqlrewrite.DB
	transaction *sqlrewrite.Tx
	committed   bool
	commit      func() error
	checkpoint  func() error
}

type databaseRewrites []*databaseRewrite

type pendingMoveDatabases struct {
	state         databaseRewrites
	memories      databaseRewrites
	queue         databaseRewrites
	gitBackup     []string
	removeAll     func(string) error
	reportWarning func(string)
}

// plannedDatabases is the set of databases a surface's plan was captured
// from. surface names them in the drift error ("state", "queue").
type plannedDatabases struct {
	surface string
	paths   []string
}

// requireDiscovered fails unless discovered names exactly the planned
// databases. A database added since the plan was never matched, and one
// removed would leave its planned rows unwritten.
func (planned plannedDatabases) requireDiscovered(discovered []string) error {
	current := make(map[string]struct{}, len(discovered))
	for _, path := range discovered {
		current[path] = struct{}{}
	}
	wasPlanned := make(map[string]struct{}, len(planned.paths))
	var removed []string
	for _, path := range planned.paths {
		wasPlanned[path] = struct{}{}
		if _, present := current[path]; !present {
			removed = append(removed, path)
		}
	}
	var added []string
	for _, path := range discovered {
		if _, ok := wasPlanned[path]; !ok {
			added = append(added, path)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}
	sort.Strings(removed)
	return fmt.Errorf("%s databases changed after the plan: added %v, removed %v", planned.surface, added, removed)
}

// startStateDBRewritesWithPlan applies plans, captured in MoveSurfaces'
// preflight, to the state databases.
func startStateDBRewritesWithPlan(
	ctx context.Context, sqliteDir, oldPath, newPath string, plans stateDBRewritePlans, undo *tool.Restorer,
) (databaseRewrites, int, error) {
	planned := &plannedDatabases{surface: "state", paths: slices.Collect(maps.Keys(plans))}
	return startDatabaseRewrites(ctx, sqliteDir, stateDBGlob, planned, oldPath, newPath,
		func(ctx context.Context, path string, database *sqlrewrite.DB, transaction *sqlrewrite.Tx, _, _ string) (int, error) {
			return rewriteStateDBPathsWithPlan(ctx, database, transaction, plans[path])
		}, undo)
}

func startMemoriesDBRewrites(ctx context.Context, sqliteDir, oldPath, newPath string, undo *tool.Restorer) (databaseRewrites, int, error) {
	return startDatabaseRewrites(ctx, sqliteDir, memoriesDBGlob, nil, oldPath, newPath, rewriteStage1TextColumns, undo)
}

// startDatabaseRewrites opens one uncommitted transaction per database
// matching glob in sqliteDir and runs rewrite inside it. A non-nil planned
// means the surface applies a plan captured in MoveSurfaces' preflight,
// which predates the writer witness and flock Apply runs under, so the
// discovered databases must first be exactly the planned ones.
func startDatabaseRewrites(
	ctx context.Context, sqliteDir, glob string, planned *plannedDatabases, oldPath, newPath string,
	rewrite func(ctx context.Context, path string, database *sqlrewrite.DB, transaction *sqlrewrite.Tx, oldPath, newPath string) (int, error),
	undo *tool.Restorer,
) (databaseRewrites, int, error) {
	paths, err := discoverDatabases(sqliteDir, glob)
	if err != nil {
		return nil, 0, err
	}
	if planned != nil {
		if err := planned.requireDiscovered(paths); err != nil {
			return nil, 0, err
		}
	}
	var rewrites databaseRewrites
	total := 0
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		database, err := sqlrewrite.Open(path)
		if err != nil {
			return nil, 0, fmt.Errorf("open %s: %w", path, err)
		}
		transaction, err := database.Begin()
		if err != nil {
			_ = database.Close()
			return nil, 0, fmt.Errorf("begin %s: %w", path, err)
		}
		pending := &databaseRewrite{
			path: path, database: database, transaction: transaction,
			commit: transaction.Commit, checkpoint: database.CheckpointTruncate,
		}
		rewrites = append(rewrites, pending)
		undo.RegisterUndo(pending.rollback)
		count, err := rewrite(ctx, path, database, transaction, oldPath, newPath)
		if err != nil {
			return nil, 0, fmt.Errorf("rewrite %s: %w", path, err)
		}
		total += count
	}
	return rewrites, total, nil
}

func (rewrite *databaseRewrite) rollback() error {
	var errs []error
	if !rewrite.committed {
		if err := rewrite.transaction.Rollback(); err != nil {
			errs = append(errs, fmt.Errorf("rollback %s: %w", rewrite.path, err))
		}
	}
	if err := rewrite.database.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close %s after rollback: %w", rewrite.path, err))
	}
	return errors.Join(errs...)
}

func (pending *pendingMoveDatabases) commitSurface() tool.Surface {
	return tool.Surface{
		Name: "commit-databases",
		Plan: func(context.Context) (tool.SurfaceResult, error) { return tool.SurfaceResult{}, nil },
		Apply: func(_ context.Context, _ *tool.Restorer) (tool.SurfaceResult, error) {
			// SQLite transactions on separate databases cannot commit atomically.
			// Commit state last because it is the database identity source; a state
			// failure leaves the project discoverable for a convergent rerun.
			commitOrder := []databaseRewrites{pending.memories, pending.queue, pending.state}
			var committedPaths []string
			for _, rewrites := range commitOrder {
				for _, rewrite := range rewrites {
					if err := rewrite.commit(); err != nil {
						return tool.SurfaceResult{}, fmt.Errorf(
							"partial database commit: %s committed; database commit failed: %s; state remains discoverable and re-running the move converges: %w",
							strings.Join(committedPaths, ", "), rewrite.path, err,
						)
					}
					rewrite.committed = true
					committedPaths = append(committedPaths, rewrite.path)
				}
			}
			for _, rewrites := range commitOrder {
				for _, rewrite := range rewrites {
					if err := rewrite.checkpoint(); err != nil {
						pending.addWarning(fmt.Sprintf("could not checkpoint %s after commit: %v", rewrite.path, err))
					}
					if err := rewrite.database.Close(); err != nil {
						pending.addWarning(fmt.Sprintf("could not close %s after commit: %v", rewrite.path, err))
					}
				}
			}
			for _, backup := range pending.gitBackup {
				removeAll := pending.removeAll
				if removeAll == nil {
					removeAll = os.RemoveAll
				}
				_ = removeAll(backup)
			}
			return tool.SurfaceResult{}, nil
		},
	}
}

func (pending *pendingMoveDatabases) addWarning(warning string) {
	// The committed data is consistent; checkpointing is maintenance that the
	// next database open folds in, so it must not trigger file restoration.
	if pending.reportWarning != nil {
		pending.reportWarning(warning)
	}
}
