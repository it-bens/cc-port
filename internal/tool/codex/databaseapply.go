package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	gitBackup     string
	removeAll     func(string) error
	reportWarning func(string)
}

func startStateDBRewrites(ctx context.Context, sqliteDir, oldPath, newPath string, undo *tool.Restorer) (databaseRewrites, int, error) {
	return startDatabaseRewrites(ctx, sqliteDir, stateDBGlob, oldPath, newPath, rewriteThreadsAndAgentJobs, undo)
}

func startMemoriesDBRewrites(ctx context.Context, sqliteDir, oldPath, newPath string, undo *tool.Restorer) (databaseRewrites, int, error) {
	return startDatabaseRewrites(ctx, sqliteDir, memoriesDBGlob, oldPath, newPath, rewriteStage1TextColumns, undo)
}

func startDatabaseRewrites(
	ctx context.Context, sqliteDir, pattern, oldPath, newPath string,
	rewrite func(ctx context.Context, path string, database *sqlrewrite.DB, transaction *sqlrewrite.Tx, oldPath, newPath string) (int, error),
	undo *tool.Restorer,
) (databaseRewrites, int, error) {
	paths, err := discoverDatabases(sqliteDir, pattern)
	if err != nil {
		return nil, 0, err
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
			// Two SQLite transactions cannot commit atomically. Commit memories
			// before state because state is the database identity source; a state
			// failure leaves the project discoverable for a convergent rerun.
			var committedPaths []string
			for _, rewrites := range []databaseRewrites{pending.memories, pending.state} {
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
			for _, rewrites := range []databaseRewrites{pending.memories, pending.state} {
				for _, rewrite := range rewrites {
					if err := rewrite.checkpoint(); err != nil {
						pending.addWarning(fmt.Sprintf("could not checkpoint %s after commit: %v", rewrite.path, err))
					}
					if err := rewrite.database.Close(); err != nil {
						pending.addWarning(fmt.Sprintf("could not close %s after commit: %v", rewrite.path, err))
					}
				}
			}
			if pending.gitBackup != "" {
				removeAll := pending.removeAll
				if removeAll == nil {
					removeAll = os.RemoveAll
				}
				_ = removeAll(pending.gitBackup)
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
