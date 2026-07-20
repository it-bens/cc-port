package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/it-bens/cc-port/internal/sqlrewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

// MoveSurfaces implements tool.Mover.
func (workspace *Workspace) MoveSurfaces(req tool.MoveRequest) ([]tool.Surface, error) {
	known, err := workspace.projectKnown(req.OldPath, req.NewPath)
	if err != nil {
		return nil, fmt.Errorf("determine project identity: %w", err)
	}
	refusalWarning, err := codexDevWarning(filepath.Join(workspace.home.Dir, "sqlite", "codex-dev.db"), req.OldPath)
	if err != nil {
		return nil, fmt.Errorf("inspect codex-dev database: %w", err)
	}
	if !known && refusalWarning == "" {
		return nil, workspace.projectAbsenceError()
	}

	workspace.clearApplyWarnings()
	pending := &pendingMoveDatabases{removeAll: os.RemoveAll, reportWarning: workspace.addApplyWarning}
	surfaces := []tool.Surface{}
	if refusalWarning != "" {
		surfaces = append(surfaces, codexDevRefusalSurface(refusalWarning))
	}
	if !known {
		return surfaces, nil
	}
	return append(surfaces,
		workspace.stateDBSurface(req, pending),
		workspace.memoriesDBSurface(req, pending),
		workspace.configSurface(req),
		workspace.rolloutsSurface(req),
		workspace.memoriesWorktreeSurface(req, pending),
		workspace.agentsMarketplaceSurface(req),
		pending.commitSurface(),
	), nil
}

// projectKnown reports whether Codex has any record of oldPath: a thread
// row, a config.toml/profile projects key, or a rollout's structured cwd.
// No identity witness is needed (spec §6.1): Codex stores cwd verbatim,
// so equality-or-prefix matching against any one source is sufficient.
// newPath is only needed to run planRolloutFile's rewrite-pipeline count
// identically to how MoveSurfaces' own rolloutsSurface will count and
// apply; this call only inspects whether that count is positive.
func (workspace *Workspace) projectKnown(oldPath, newPath string) (bool, error) {
	stateKnown, err := stateDBKnowsProject(workspace.home.SQLiteDir, oldPath)
	if err != nil {
		return false, err
	}
	if stateKnown {
		return true, nil
	}

	configKnown, err := configTOMLKnowsProject(workspace.home, oldPath)
	if err != nil {
		return false, err
	}
	if configKnown {
		return true, nil
	}

	rolloutFiles, err := discoverRolloutFiles(workspace.home)
	if err != nil {
		return false, err
	}
	for _, path := range rolloutFiles {
		count, eraA, err := planRolloutFile(path, oldPath, newPath, false, workspace.transcodeCaps)
		if err != nil {
			return false, fmt.Errorf("%s: %w", path, err)
		}
		if !eraA && count > 0 {
			return true, nil
		}
	}

	return false, nil
}

func sqlDatabaseSurface(
	name string,
	req tool.MoveRequest,
	count func(ctx context.Context, oldPath, newPath string) (int, error),
	start func(ctx context.Context, oldPath, newPath string, undo *tool.Restorer) (databaseRewrites, int, error),
	assign func(databaseRewrites),
) tool.Surface {
	return tool.Surface{
		Name: name,
		Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
			if err := ctx.Err(); err != nil {
				return tool.SurfaceResult{}, err
			}
			changed, err := count(ctx, req.OldPath, req.NewPath)
			return tool.SurfaceResult{Count: changed}, err
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
			if err := ctx.Err(); err != nil {
				return tool.SurfaceResult{}, err
			}
			rewrites, changed, err := start(ctx, req.OldPath, req.NewPath, undo)
			if err != nil {
				return tool.SurfaceResult{}, err
			}
			assign(rewrites)
			return tool.SurfaceResult{Count: changed}, nil
		},
	}
}

func (workspace *Workspace) stateDBSurface(req tool.MoveRequest, pending *pendingMoveDatabases) tool.Surface {
	return sqlDatabaseSurface("state-db", req,
		func(ctx context.Context, oldPath, newPath string) (int, error) {
			return countStateDB(ctx, workspace.home.SQLiteDir, oldPath, newPath)
		},
		func(ctx context.Context, oldPath, newPath string, undo *tool.Restorer) (databaseRewrites, int, error) {
			return startStateDBRewrites(ctx, workspace.home.SQLiteDir, oldPath, newPath, undo)
		},
		func(rewrites databaseRewrites) { pending.state = rewrites },
	)
}

func (workspace *Workspace) memoriesDBSurface(req tool.MoveRequest, pending *pendingMoveDatabases) tool.Surface {
	return sqlDatabaseSurface("memories-db", req,
		func(ctx context.Context, oldPath, newPath string) (int, error) {
			return countMemoriesDB(ctx, workspace.home.SQLiteDir, oldPath, newPath)
		},
		func(ctx context.Context, oldPath, newPath string, undo *tool.Restorer) (databaseRewrites, int, error) {
			return startMemoriesDBRewrites(ctx, workspace.home.SQLiteDir, oldPath, newPath, undo)
		},
		func(rewrites databaseRewrites) { pending.memories = rewrites },
	)
}

func (workspace *Workspace) configSurface(req tool.MoveRequest) tool.Surface {
	return tool.Surface{
		Name: "config",
		Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
			if err := ctx.Err(); err != nil {
				return tool.SurfaceResult{}, err
			}
			files, err := discoverConfigTOMLFiles(workspace.home)
			if err != nil {
				return tool.SurfaceResult{}, err
			}
			total := 0
			for _, path := range files {
				count, err := planConfigTOMLFile(path, req.OldPath, req.NewPath)
				if err != nil {
					return tool.SurfaceResult{}, err
				}
				total += count
			}
			return tool.SurfaceResult{Count: total}, nil
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
			files, err := discoverConfigTOMLFiles(workspace.home)
			if err != nil {
				return tool.SurfaceResult{}, err
			}
			total := 0
			for _, path := range files {
				if err := ctx.Err(); err != nil {
					return tool.SurfaceResult{}, err
				}
				count, err := applyConfigTOMLFile(path, req.OldPath, req.NewPath, undo)
				if err != nil {
					return tool.SurfaceResult{}, err
				}
				total += count
			}
			return tool.SurfaceResult{Count: total}, nil
		},
	}
}

func (workspace *Workspace) rolloutsSurface(req tool.MoveRequest) tool.Surface {
	return tool.Surface{
		Name: categorySessions,
		Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
			files, err := discoverRolloutFiles(workspace.home)
			if err != nil {
				return tool.SurfaceResult{}, err
			}
			total := 0
			for _, path := range files {
				if err := ctx.Err(); err != nil {
					return tool.SurfaceResult{}, err
				}
				count, eraA, err := planRolloutFile(path, req.OldPath, req.NewPath, req.DeepRewrite, workspace.transcodeCaps)
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("%s: %w", path, err)
				}
				if eraA {
					continue
				}
				total += count
			}
			return tool.SurfaceResult{Count: total}, nil
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
			files, err := discoverRolloutFiles(workspace.home)
			if err != nil {
				return tool.SurfaceResult{}, err
			}
			total := 0
			for _, path := range files {
				if err := ctx.Err(); err != nil {
					return tool.SurfaceResult{}, err
				}
				planCount, eraA, err := planRolloutFile(path, req.OldPath, req.NewPath, req.DeepRewrite, workspace.transcodeCaps)
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("%s: %w", path, err)
				}
				if eraA || planCount == 0 {
					continue
				}
				if err := undo.RegisterFile(path); err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("back up %s: %w", path, err)
				}
				changed, _, err := applyRolloutFile(ctx, path, req.OldPath, req.NewPath, req.DeepRewrite, workspace.transcodeCaps)
				if err != nil {
					return tool.SurfaceResult{}, fmt.Errorf("%s: %w", path, err)
				}
				total += changed
			}
			return tool.SurfaceResult{Count: total}, nil
		},
	}
}

func (workspace *Workspace) memoriesWorktreeSurface(req tool.MoveRequest, pending *pendingMoveDatabases) tool.Surface {
	root := filepath.Join(workspace.home.Dir, memoriesWorktreeSubdir)
	return tool.Surface{
		Name: "memories-worktree",
		Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
			if err := ctx.Err(); err != nil {
				return tool.SurfaceResult{}, err
			}
			count, err := planMemoriesWorktree(root, req.OldPath)
			return tool.SurfaceResult{Count: count}, err
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
			if err := reconcileStrandedGitBackup(root); err != nil {
				return tool.SurfaceResult{}, err
			}
			count, err := applyMemoriesWorktree(ctx, root, req.OldPath, req.NewPath, undo)
			if err != nil {
				return tool.SurfaceResult{}, err
			}
			if count == 0 {
				return tool.SurfaceResult{Count: count}, nil
			}
			backup, err := moveGitBaselineToBackup(root, undo)
			if err != nil {
				return tool.SurfaceResult{Count: count}, err
			}
			pending.gitBackup = backup
			return tool.SurfaceResult{Count: count}, nil
		},
	}
}

// moveGitBaselineToBackup renames root/.git to a sibling backup only when
// hasNoRemoteGitBaseline confirms the shape probe. commitSurface removes the
// backup after every database transaction commits.
func moveGitBaselineToBackup(root string, undo *tool.Restorer) (string, error) {
	if _, err := os.Stat(filepath.Join(root, gitDirName)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat %s: %w", filepath.Join(root, gitDirName), err)
	}
	safe, err := hasNoRemoteGitBaseline(root)
	if err != nil {
		return "", err
	}
	if !safe {
		return "", nil
	}
	gitPath := filepath.Join(root, gitDirName)
	backup := gitPath + gitBackupSuffix
	if _, err := os.Stat(backup); err == nil {
		return "", fmt.Errorf("rollback backup %s already exists", backup)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("stat rollback backup %s: %w", backup, err)
	}
	undo.RegisterUndo(func() error { return os.Rename(backup, gitPath) })
	if err := os.Rename(gitPath, backup); err != nil {
		return "", fmt.Errorf("rename %s to rollback backup: %w", gitPath, err)
	}
	return backup, nil
}

func (workspace *Workspace) agentsMarketplaceSurface(req tool.MoveRequest) tool.Surface {
	return tool.Surface{
		Name: "agents-marketplace",
		Plan: func(ctx context.Context) (tool.SurfaceResult, error) {
			if err := ctx.Err(); err != nil {
				return tool.SurfaceResult{}, err
			}
			count, err := planAgentsMarketplace(workspace.home.AgentsDir, req.OldPath)
			return tool.SurfaceResult{Count: count}, err
		},
		Apply: func(ctx context.Context, undo *tool.Restorer) (tool.SurfaceResult, error) {
			if err := ctx.Err(); err != nil {
				return tool.SurfaceResult{}, err
			}
			count, err := applyAgentsMarketplace(workspace.home.AgentsDir, req.OldPath, req.NewPath, undo)
			return tool.SurfaceResult{Count: count}, err
		},
	}
}

// ResidualWarnings implements tool.Mover: the checkpoint/close warnings
// recorded during the preceding Apply, plus content a move preserves
// verbatim or leaves untouched by design. On a residual-scan error, the
// warnings collected so far are returned alongside the error rather than
// discarded, since Apply already recorded those checkpoint warnings.
func (workspace *Workspace) ResidualWarnings(req tool.MoveRequest) ([]string, error) {
	warnings := workspace.applyWarningSnapshot()

	eraAWarning, err := workspace.eraAWarning(req.OldPath, req.NewPath)
	if err != nil {
		return warnings, err
	}
	if eraAWarning != "" {
		warnings = append(warnings, eraAWarning)
	}

	marketplaceWarning, err := marketplaceUnparseableWarning(workspace.home.AgentsDir)
	if err != nil {
		return warnings, err
	}
	if marketplaceWarning != "" {
		warnings = append(warnings, marketplaceWarning)
	}

	agentsWarning, err := residualAgentsWarning(workspace.home.AgentsDir, req.OldPath)
	if err != nil {
		return warnings, err
	}
	if agentsWarning != "" {
		warnings = append(warnings, agentsWarning)
	}

	gitWarning, err := memoriesGitBaselineWarning(filepath.Join(workspace.home.Dir, memoriesWorktreeSubdir))
	if err != nil {
		return warnings, err
	}
	if gitWarning != "" {
		warnings = append(warnings, gitWarning)
	}

	goalsWarning, err := goalsWarning(workspace.home.SQLiteDir)
	if err != nil {
		return warnings, err
	}
	if goalsWarning != "" {
		warnings = append(warnings, goalsWarning)
	}

	codexDevWarning, err := codexDevWarning(filepath.Join(workspace.home.Dir, "sqlite", "codex-dev.db"), req.OldPath)
	if err != nil {
		return warnings, err
	}
	if codexDevWarning != "" {
		warnings = append(warnings, codexDevWarning)
	}

	backupWarning, err := gitBackupWarning(filepath.Join(workspace.home.Dir, memoriesWorktreeSubdir, gitDirName+gitBackupSuffix))
	if err != nil {
		return warnings, err
	}
	if backupWarning != "" {
		warnings = append(warnings, backupWarning)
	}

	sqliteHomeWarning, err := profileSQLiteHomeWarning(workspace.home, workspace.getenv)
	if err != nil {
		return warnings, err
	}
	if sqliteHomeWarning != "" {
		warnings = append(warnings, sqliteHomeWarning)
	}

	return warnings, nil
}

func (workspace *Workspace) clearApplyWarnings() {
	workspace.warningMutex.Lock()
	defer workspace.warningMutex.Unlock()
	workspace.applyWarnings = nil
}

func (workspace *Workspace) addApplyWarning(warning string) {
	workspace.warningMutex.Lock()
	defer workspace.warningMutex.Unlock()
	workspace.applyWarnings = append(workspace.applyWarnings, warning)
}

func (workspace *Workspace) applyWarningSnapshot() []string {
	workspace.warningMutex.Lock()
	defer workspace.warningMutex.Unlock()
	return append([]string(nil), workspace.applyWarnings...)
}

func goalsWarning(sqliteDir string) (string, error) {
	databases, err := discoverDatabases(sqliteDir, goalsDBGlob)
	if err != nil {
		return "", err
	}
	for _, path := range databases {
		hasRows, err := goalsDatabaseHasRows(path)
		if err != nil {
			return "", fmt.Errorf("inspect goals database %s: %w", path, err)
		}
		if hasRows {
			return "goals present, not ported", nil
		}
	}
	return "", nil
}

func codexDevWarning(path, oldPath string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = database.Close() }()

	count := 0
	for _, column := range []struct {
		table, column string
		countMatches  func(database *sql.DB, table, column, oldPath string) (int, error)
	}{
		// automations.cwds is free-text/multi-value (plural name), not a
		// single verbatim cwd per row, so it stays on CountTextColumnRO's
		// boundary-aware substring scan. automation_runs.source_cwd and
		// local_thread_catalog.cwd each hold exactly one cwd per row, like
		// threads.cwd, so they route through the same canonical matching
		// (spec §5.1): a symlink-aliased value is detected, not missed.
		{table: "automations", column: "cwds", countMatches: sqlrewrite.CountTextColumnRO},
		{table: "automation_runs", column: "source_cwd", countMatches: countMatchingColumnRowsBackground},
		{table: "local_thread_catalog", column: "cwd", countMatches: countMatchingColumnRowsBackground},
	} {
		if err := requireTableColumn(database, column.table, column.column); err != nil {
			return fmt.Sprintf("codex-dev.db schema drift (%v); refusing to move", err), nil
		}
		matches, queryErr := column.countMatches(database, column.table, column.column, oldPath)
		if queryErr != nil {
			return "", fmt.Errorf("inspect %s.%s in %s: %w", column.table, column.column, path, queryErr)
		}
		count += matches
	}
	if count == 0 {
		return "", nil
	}
	return "codex-dev.db contains path references to the moved project and is never rewritten; refusing to move", nil
}

func codexDevRefusalSurface(warning string) tool.Surface {
	return tool.Surface{
		Name: "codex-dev-preflight",
		Plan: func(context.Context) (tool.SurfaceResult, error) { return tool.SurfaceResult{}, nil },
		Apply: func(context.Context, *tool.Restorer) (tool.SurfaceResult, error) {
			return tool.SurfaceResult{}, errors.New(warning)
		},
	}
}

func gitBackupWarning(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat git baseline rollback backup %s: %w", path, err)
	}
	return fmt.Sprintf("could not remove git baseline rollback backup %s; it was left in place", path), nil
}

func (workspace *Workspace) eraAWarning(oldPath, newPath string) (string, error) {
	files, err := discoverRolloutFiles(workspace.home)
	if err != nil {
		return "", err
	}
	count := 0
	for _, path := range files {
		_, eraA, err := planRolloutFile(path, oldPath, newPath, false, workspace.transcodeCaps)
		if err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		if eraA {
			count++
		}
	}
	if count == 0 {
		return "", nil
	}
	return fmt.Sprintf(
		"%d rollout(s) predate structured cwd tracking and were left untouched (no session_meta or turn_context line to anchor a safe rewrite)",
		count,
	), nil
}

func marketplaceUnparseableWarning(agentsDir string) (string, error) {
	data, ok, err := readAgentsMarketplace(agentsDir)
	if err != nil || !ok {
		return "", err
	}
	var document any
	if json.Unmarshal(data, &document) == nil {
		return "", nil
	}
	return "~/.agents/plugins/marketplace.json is not valid JSON; left untouched", nil
}

func memoriesGitBaselineWarning(root string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, gitDirName)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat %s: %w", filepath.Join(root, gitDirName), err)
	}
	safe, err := hasNoRemoteGitBaseline(root)
	if err != nil {
		return "", err
	}
	if safe {
		return "", nil
	}
	return "memories/.git carries a remote and was left in place; its worktree contents were rewritten", nil
}
