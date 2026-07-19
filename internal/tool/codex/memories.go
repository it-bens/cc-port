package codex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/sqlrewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

// memoriesWorktreeSubdir is $CODEX_HOME/memories (memories/write/src/lib.rs:117,
// memory_root).
const memoriesWorktreeSubdir = "memories"

// gitDirName is the git-baseline metadata directory move must never
// rewrite bytes inside, and may move to a rollback backup only behind the probe in
// hasNoRemoteGitBaseline.
const gitDirName = ".git"

// gitBackupSuffix reuses rewrite.RollbackSuffix: both the Restorer's file
// sibling and this git baseline backup are cc-port rollback artifacts,
// single-sourced under one constant.
const gitBackupSuffix = rewrite.RollbackSuffix

// Depended-on stage1_outputs columns (state/memory_migrations/0001_memories.sql):
// raw_memory and rollout_summary hold natural-language prose that can
// embed the project's cwd; rollout_slug is an algorithmically derived
// filename slug (thread id / timestamp / hash), never the raw path, so it
// is not rewritten.
const (
	stage1OutputsTable         = "stage1_outputs"
	stage1ThreadIDColumn       = "thread_id"
	stage1RawMemoryColumn      = "raw_memory"
	stage1RolloutSummaryColumn = "rollout_summary"
)

// countMemoriesDB reports how many text-column occurrences a move would
// rewrite across every discovered memories_*.sqlite database. It uses
// read-only SELECT counts: raw_memory and rollout_summary are free-text prose,
// so countTextRows performs the same boundary-aware path scan as Apply's
// RewriteTextColumn logic rather than the exact/prefix predicate used for
// path-shaped columns.
func countMemoriesDB(sqliteDir, oldPath, newPath string) (int, error) {
	databases, err := discoverDatabases(sqliteDir, memoriesDBGlob)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, path := range databases {
		count, err := countMemoriesDBFile(path, oldPath, newPath)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", path, err)
		}
		total += count
	}
	return total, nil
}

func countMemoriesDBFile(path, oldPath, _ string) (int, error) {
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = database.Close() }()

	return countMemoriesDBReadOnly(database, oldPath)
}

func countMemoriesDBReadOnly(database *sql.DB, oldPath string) (int, error) {
	total := 0
	for _, column := range []string{stage1RawMemoryColumn, stage1RolloutSummaryColumn} {
		count, err := countTextRows(database, stage1OutputsTable, column, oldPath)
		if err != nil {
			return 0, fmt.Errorf("count stage1_outputs.%s: %w", column, err)
		}
		total += count
	}
	return total, nil
}

func rewriteStage1TextColumns(database *sqlrewrite.DB, transaction *sqlrewrite.Tx, oldPath, newPath string) (int, error) {
	total := 0
	for _, column := range []string{stage1RawMemoryColumn, stage1RolloutSummaryColumn} {
		count, err := database.RewriteTextColumn(transaction, stage1OutputsTable, stage1ThreadIDColumn, column, oldPath, newPath)
		if err != nil {
			return 0, fmt.Errorf("rewrite stage1_outputs.%s: %w", column, err)
		}
		total += count
	}
	return total, nil
}

// worktreeFiles returns every regular file under root except anything
// under a .git directory; the git object store is never touched at the
// byte level (docs/architecture.md §Git-repo-in-state policy).
func worktreeFiles(root string) ([]string, error) {
	var files []string
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			if entry.Name() == gitDirName {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, path)
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("walk %s: %w", root, walkErr)
	}
	return files, nil
}

// planMemoriesWorktree reports how many bounded occurrences a move would
// rewrite across every memories/ worktree file (raw_memories.md,
// rollout_summaries/*.md, extensions/…), outside .git.
func planMemoriesWorktree(root, oldPath string) (int, error) {
	files, err := worktreeFiles(root)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, path := range files {
		data, err := os.ReadFile(path) //nolint:gosec // G304: path from adapter-controlled worktree walk
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", path, err)
		}
		total += rewrite.CountPathInBytes(data, oldPath)
	}
	return total, nil
}

// applyMemoriesWorktree rewrites every memories/ worktree file in place.
func applyMemoriesWorktree(ctx context.Context, root, oldPath, newPath string, undo *tool.Restorer) (int, error) {
	files, err := worktreeFiles(root)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return 0, fmt.Errorf("stat %s: %w", path, err)
		}
		if err := undo.RegisterFile(path); err != nil {
			return 0, fmt.Errorf("back up %s: %w", path, err)
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: path from adapter-controlled worktree walk
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", path, err)
		}
		rewritten, count := rewrite.ReplacePathInBytes(data, oldPath, newPath)
		if count > 0 {
			if err := rewrite.SafeWriteFile(path, rewritten, info.Mode()); err != nil {
				return 0, fmt.Errorf("write %s: %w", path, err)
			}
		}
		total += count
	}
	return total, nil
}

// reconcileStrandedGitBackup removes a rollback backup left by a crash before
// its post-commit cleanup. This run has not renamed its baseline yet, so the
// backup is never needed for this run's rollback.
func reconcileStrandedGitBackup(root string) error {
	backup := filepath.Join(root, gitDirName) + gitBackupSuffix
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("reconcile stranded git backup %s: %w", backup, err)
	}
	return nil
}

// hasNoRemoteGitBaseline implements the §4.4 shape probe: memories/.git/config
// exists and contains no "[remote" section. This is cc-port's own heuristic
// for "Codex provably re-initializes a missing .git" — the underlying fact
// it stands in for is ensure_git_baseline_repository unconditionally
// resetting a missing or unusable baseline (git-utils/src/baseline.rs:78-92,
// invoked from memories/write/src/workspace.rs:18) — not something Codex's
// own source encodes as a "[remote" check itself.
func hasNoRemoteGitBaseline(root string) (bool, error) {
	configPath := filepath.Join(root, gitDirName, "config")
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: path constructed from resolved codex home
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", configPath, err)
	}
	return !strings.Contains(string(data), "[remote"), nil
}
