package move

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/fsutil"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// executeMove performs the copy-verify-delete sequence on disk after the
// preflight checks in Apply have passed. It owns the rollback of partial
// state: on any failure, newly created paths are removed and any globally
// modified files are restored from the tracker.
func executeMove(
	ctx context.Context,
	claudeHome *claude.Home,
	locations *claude.ProjectLocations,
	oldProjectDir, newProjectDir string,
	moveOptions Options,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	var createdPaths []string
	success := false
	defer func() {
		if !success {
			for i := len(createdPaths) - 1; i >= 0; i-- {
				_ = os.RemoveAll(createdPaths[i])
			}
		}
	}()

	createdPaths = append(createdPaths, newProjectDir)
	if err := fsutil.CopyDir(ctx, oldProjectDir, newProjectDir); err != nil {
		return fmt.Errorf("copy project directory: %w", err)
	}

	tracker := &globalFileTracker{}

	if err := rewriteNewProjectDir(ctx, newProjectDir, moveOptions); err != nil {
		return err
	}

	if err := rewriteGlobalFiles(ctx, claudeHome, locations, moveOptions, tracker); err != nil {
		return errors.Join(err, tracker.restore())
	}

	if !moveOptions.RefsOnly {
		createdPaths = append(createdPaths, moveOptions.NewPath)
		if err := fsutil.CopyDir(ctx, moveOptions.OldPath, moveOptions.NewPath); err != nil {
			return errors.Join(fmt.Errorf("copy project on disk: %w", err), tracker.restore())
		}
	}

	if err := verifyNewDirs(newProjectDir, moveOptions); err != nil {
		return errors.Join(err, tracker.restore())
	}

	if err := deleteOriginals(oldProjectDir, moveOptions, tracker); err != nil {
		return err
	}

	warnFileHistoryPreserved(ctx, locations, moveOptions)

	tracker.cleanupSiblings()
	success = true
	return nil
}

// siblingSuffix names the sibling rollback file that saveToSibling writes
// next to any large tracked target. Kept as a constant so the test package
// and rewriteHistoryFile agree on the name without duplicating the string.
const siblingSuffix = ".cc-port-rollback.tmp"

// siblingBackupThreshold is the size above which rewriteHistoryFile routes
// the rollback snapshot through saveToSibling (on-disk streamed backup)
// rather than save (in-memory bytes). 1 MiB; see spec §Move rollback tracker.
const siblingBackupThreshold = 1 << 20

// globalFileTracker records the original contents of global files so they can
// be restored if Apply fails partway through. Two storage shapes are
// supported: in-memory byte snapshots (save) for small files, and sibling
// rollback files (saveToSibling) for large streamed rewrites where holding
// the original in RAM would defeat the streaming-I/O promise.
type globalFileTracker struct {
	saved []savedFile
}

// savedFile records one tracked rollback target. Exactly one of data or
// sibling carries the backup payload: when sibling is non-empty, restore
// renames that file back onto path; otherwise it writes data through
// SafeWriteFile.
type savedFile struct {
	path    string
	data    []byte
	mode    os.FileMode
	sibling string
}

func (t *globalFileTracker) save(path string) (data []byte, mode os.FileMode, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	data, err = os.ReadFile(path) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return nil, 0, err
	}
	t.saved = append(t.saved, savedFile{path: path, data: data, mode: info.Mode()})
	return data, info.Mode(), nil
}

// saveToSibling streams path into a sibling rollback file and registers it
// with the tracker. The sibling lives on the same filesystem as path so
// restore can rename it back atomically on failure; cleanupSiblings removes
// it once Apply succeeds. Streaming keeps peak memory bounded by the
// io.Copy buffer, not by the source file's total size.
func (t *globalFileTracker) saveToSibling(path string) (sibling string, mode os.FileMode, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("stat %s: %w", path, err)
	}
	source, err := os.Open(path) //nolint:gosec // G304: path constructed from trusted internal data
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = source.Close() }()

	sibling = path + siblingSuffix
	//nolint:gosec // G304: sibling path constructed from trusted internal data
	destination, err := os.OpenFile(sibling, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return "", 0, fmt.Errorf("create %s: %w", sibling, err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		_ = os.Remove(sibling)
		return "", 0, fmt.Errorf("copy %s to %s: %w", path, sibling, err)
	}
	if err := destination.Close(); err != nil {
		_ = os.Remove(sibling)
		return "", 0, fmt.Errorf("close %s: %w", sibling, err)
	}

	t.saved = append(t.saved, savedFile{path: path, mode: info.Mode(), sibling: sibling})
	return sibling, info.Mode(), nil
}

// cleanupSiblings removes every registered sibling rollback file. Called
// from executeMove once success is certain. In-memory savedFile entries
// (sibling == "") are skipped. os.Remove on an already-absent sibling is
// swallowed because the post-condition (sibling absent) holds either way.
func (t *globalFileTracker) cleanupSiblings() {
	for _, s := range t.saved {
		if s.sibling == "" {
			continue
		}
		// sibling already gone: nothing to remove
		_ = os.Remove(s.sibling)
	}
}

func (t *globalFileTracker) restore() error {
	var errs []error
	for _, s := range t.saved {
		if s.sibling != "" {
			if err := os.Rename(s.sibling, s.path); err != nil {
				errs = append(errs, fmt.Errorf("restore %s from sibling %s: %w", s.path, s.sibling, err))
			}
			continue
		}
		if err := rewrite.SafeWriteFile(s.path, s.data, s.mode); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", s.path, err))
		}
	}
	return errors.Join(errs...)
}

// checkEncodedDirCollision refuses moves that would overwrite existing encoded
// storage or collapse old and new onto the same directory.
// see internal/claude/README.md §Path encoding for the lossy encoding the check defends against.
func checkEncodedDirCollision(claudeHome *claude.Home, oldPath, newPath string) error {
	oldEncodedDir := claudeHome.ProjectDir(oldPath)
	newEncodedDir := claudeHome.ProjectDir(newPath)

	if oldEncodedDir == newEncodedDir {
		return fmt.Errorf(
			"refusing to move: %q and %q both encode to directory %s — "+
				"the encoder is lossy on '/', '.', and ' ', so both paths share on-disk storage",
			oldPath, newPath, filepath.Base(newEncodedDir),
		)
	}

	if _, err := os.Stat(newEncodedDir); err == nil {
		return fmt.Errorf(
			"refusing to move: new project directory %s already exists — "+
				"another real project path encodes to the same name",
			newEncodedDir,
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat new project directory %s: %w", newEncodedDir, err)
	}

	return nil
}

func verifyNewDirs(newProjectDir string, moveOptions Options) error {
	if _, err := os.Stat(newProjectDir); err != nil {
		return fmt.Errorf("verify new project data dir: %w", err)
	}
	if !moveOptions.RefsOnly {
		if _, err := os.Stat(moveOptions.NewPath); err != nil {
			return fmt.Errorf("verify new project dir on disk: %w", err)
		}
	}
	return nil
}

func deleteOriginals(oldProjectDir string, moveOptions Options, tracker *globalFileTracker) error {
	if err := os.RemoveAll(oldProjectDir); err != nil {
		return errors.Join(fmt.Errorf("remove old project data dir: %w", err), tracker.restore())
	}
	if !moveOptions.RefsOnly {
		if err := os.RemoveAll(moveOptions.OldPath); err != nil {
			// Encoded dir is already gone; we cannot resurrect it. Globals
			// restore to the old path so the user can continue working there.
			// cc-port move cannot retry because LocateProject hard-requires
			// the encoded dir — recovery is manual from here.
			residual := fmt.Errorf(
				"remove old project dir %s: %w\n"+
					"state: encoded project data (session history, memory, todos) is gone and cannot be recovered; "+
					"on-disk dir still present at %s with source files intact; "+
					"global references restored to the old path\n"+
					"recover: cc-port move cannot retry — the encoded source is gone. "+
					"Leave the project at %s (Claude Code will recreate encoded state on next session), "+
					"or complete the filesystem move with `mv %s %s`",
				moveOptions.OldPath, err,
				moveOptions.OldPath,
				moveOptions.OldPath,
				moveOptions.OldPath, moveOptions.NewPath,
			)
			return errors.Join(residual, tracker.restore())
		}
	}
	return nil
}

// listTranscriptFiles returns every file under projectDir that
// RewriteTranscripts should rewrite: top-level `.jsonl` files, plus every file
// under each session subdirectory (covering <uuid>/subagents/*.jsonl and
// <uuid>/session-memory/**).
//
// `memory/` is excluded because rewriteMemoryFilesInDir handles it separately.
// `sessions/` is excluded because rewriteSessionFiles in rewrite_global.go
// already rewrites those files; listing them here would double-rewrite them.
func listTranscriptFiles(ctx context.Context, projectDir string) ([]string, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("read project directory: %w", err)
	}

	var transcripts []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		fullPath := filepath.Join(projectDir, name)
		if !entry.IsDir() {
			if strings.HasSuffix(name, ".jsonl") {
				transcripts = append(transcripts, fullPath)
			}
			continue
		}
		if name == "memory" || name == "sessions" {
			continue
		}
		subdirFiles, err := listFilesRecursive(ctx, fullPath)
		if err != nil {
			return nil, err
		}
		transcripts = append(transcripts, subdirFiles...)
	}
	return transcripts, nil
}

// listFilesRecursive returns every file path under dir, skipping directories.
// ctx is checked at the top of every WalkDir callback so a canceled
// context aborts a long enumeration within one iteration.
func listFilesRecursive(ctx context.Context, dir string) ([]string, error) {
	var files []string
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, walkErr)
	}
	return files, nil
}

// snapshotPaths returns every snapshot file path under locations.FileHistoryDirs.
// Snapshot contents are not read; path discovery only matches the opaque-bytes
// invariant. Used by DryRun for the plan count and by Apply's preservation
// warning so both stay in lock-step with one enumeration. ctx is checked at
// the top of the outer loop as well as inside each listFilesRecursive walk.
func snapshotPaths(ctx context.Context, locations *claude.ProjectLocations) ([]string, error) {
	var paths []string
	for _, fileHistoryDir := range locations.FileHistoryDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snapshots, err := listFilesRecursive(ctx, fileHistoryDir)
		if err != nil {
			return nil, fmt.Errorf("walk file-history dir %s: %w", fileHistoryDir, err)
		}
		paths = append(paths, snapshots...)
	}
	return paths, nil
}

// resolveWarningWriter returns the writer to which Apply sends
// human-readable warnings. Defaults to os.Stderr so unconfigured callers
// still see warnings; tests inject a buffer via Options.WarningWriter.
func resolveWarningWriter(moveOptions Options) io.Writer {
	if moveOptions.WarningWriter != nil {
		return moveOptions.WarningWriter
	}
	return os.Stderr
}

// warnFileHistoryPreserved emits a warning when the project has file-history
// snapshots. Snapshot contents are preserved verbatim; the warning surfaces
// that the old project path may still appear inside them after a move.
func warnFileHistoryPreserved(
	ctx context.Context, locations *claude.ProjectLocations, moveOptions Options,
) {
	paths, err := snapshotPaths(ctx, locations)
	if err != nil {
		// snapshotPaths re-walks dirs that rewriteGlobalFiles already walked
		// successfully; an error here is unexpected. Skip the warning rather
		// than fail the apply — the move itself is complete.
		return
	}
	if len(paths) == 0 {
		return
	}
	_, _ = fmt.Fprintf(
		resolveWarningWriter(moveOptions),
		"warning: %d file-history snapshot(s) preserved as-is — contents may still reference the old project path "+
			"(used for in-session rewinds, not persisted data)\n",
		len(paths),
	)
}
