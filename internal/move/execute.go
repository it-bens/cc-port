package move

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/fsutil"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// ErrEncodedDirAmbiguous is returned by checkEncodedDirCollision when the old
// and new project paths both encode to the same on-disk storage directory. The
// encoder is lossy on '/', '.', and ' '. Callers discriminate via errors.Is.
var ErrEncodedDirAmbiguous = errors.New("refusing to move: old and new paths encode to the same directory")

// ErrEncodedDirCollision is returned by checkEncodedDirCollision when the new
// project's encoded directory already exists because another real path encodes
// to the same name. Callers discriminate via errors.Is.
var ErrEncodedDirCollision = errors.New("refusing to move: new project directory already exists")

// ErrResidualSourceDir is returned by deleteOriginals when the encoded project
// data is already removed but the on-disk source directory cannot be deleted.
// The wrapping error preserves the os cause and the recovery instructions;
// callers discriminate via errors.Is.
var ErrResidualSourceDir = errors.New("on-disk source directory still present")

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
	copyDataPhase := moveOptions.Reporter.Phase("copy project data", 0, progress.UnitFiles)
	copiedDataFiles := int64(0)
	onDataEntry := func() {
		copiedDataFiles++
		copyDataPhase.Advance(1)
	}
	if err := fsutil.CopyDir(ctx, oldProjectDir, newProjectDir, onDataEntry); err != nil {
		return fmt.Errorf("copy project directory: %w", err)
	}
	copyDataPhase.End(fmt.Sprintf("%d files", copiedDataFiles))

	tracker := &globalFileTracker{}

	rewriteDataPhase := moveOptions.Reporter.Phase("rewrite project data", 0, progress.UnitItems)
	if err := rewriteNewProjectDir(ctx, oldProjectDir, newProjectDir, moveOptions, rewriteDataPhase); err != nil {
		return err
	}
	rewriteDataPhase.End("")

	rewriteRefsPhase := moveOptions.Reporter.Phase("rewrite global references", 0, progress.UnitItems)
	if err := rewriteGlobalFiles(ctx, claudeHome, locations, moveOptions, tracker, rewriteRefsPhase); err != nil {
		return errors.Join(err, tracker.restore())
	}
	rewriteRefsPhase.End("")

	if !moveOptions.RefsOnly {
		createdPaths = append(createdPaths, moveOptions.NewPath)
		copyDirPhase := moveOptions.Reporter.Phase("copy project directory", 0, progress.UnitFiles)
		copiedDirFiles := int64(0)
		onDirEntry := func() {
			copiedDirFiles++
			copyDirPhase.Advance(1)
		}
		if err := fsutil.CopyDir(ctx, moveOptions.OldPath, moveOptions.NewPath, onDirEntry); err != nil {
			return errors.Join(fmt.Errorf("copy project on disk: %w", err), tracker.restore())
		}
		copyDirPhase.End(fmt.Sprintf("%d files", copiedDirFiles))
	}

	verifyPhase := moveOptions.Reporter.Phase("verify", 0, progress.UnitItems)
	if err := verifyNewDirs(newProjectDir, moveOptions); err != nil {
		return errors.Join(err, tracker.restore())
	}
	verifyPhase.End("new directories present")

	deletePhase := moveOptions.Reporter.Phase("delete", 0, progress.UnitItems)
	if err := deleteOriginals(oldProjectDir, moveOptions, tracker); err != nil {
		return err
	}
	deletePhase.End("originals removed")

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
			"%w: %q and %q both encode to directory %s — "+
				"the encoder is lossy on '/', '.', and ' ', so both paths share on-disk storage",
			ErrEncodedDirAmbiguous, oldPath, newPath, filepath.Base(newEncodedDir),
		)
	}

	if _, err := os.Stat(newEncodedDir); err == nil {
		return fmt.Errorf(
			"%w: %s — another real project path encodes to the same name",
			ErrEncodedDirCollision, newEncodedDir,
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
				"%w: remove old project dir %s: %w\n"+
					"state: encoded project data (session history, memory, todos) is gone and cannot be recovered; "+
					"on-disk dir still present at %s with source files intact; "+
					"global references restored to the old path\n"+
					"recover: cc-port move cannot retry — the encoded source is gone. "+
					"Leave the project at %s (Claude Code will recreate encoded state on next session), "+
					"or complete the filesystem move with `mv %s %s`",
				ErrResidualSourceDir, moveOptions.OldPath, err,
				moveOptions.OldPath,
				moveOptions.OldPath,
				moveOptions.OldPath, moveOptions.NewPath,
			)
			return errors.Join(residual, tracker.restore())
		}
	}
	return nil
}

// snapshotPaths returns every snapshot file path under locations.FileHistoryDirs.
// Snapshot contents are not read; path discovery only matches the opaque-bytes
// invariant. Used by DryRun for the plan count and by Apply's preservation
// warning so both stay in lock-step with one enumeration. ctx is checked at
// the top of the outer loop as well as inside each fsutil.ListFilesRecursive walk.
func snapshotPaths(ctx context.Context, locations *claude.ProjectLocations) ([]string, error) {
	var paths []string
	for _, fileHistoryDir := range locations.FileHistoryDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snapshots, err := fsutil.ListFilesRecursive(ctx, fileHistoryDir)
		if err != nil {
			return nil, fmt.Errorf("walk file-history dir %s: %w", fileHistoryDir, err)
		}
		paths = append(paths, snapshots...)
	}
	return paths, nil
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
	moveOptions.Reporter.Warn(fmt.Errorf(
		"note: %d file-history snapshot(s) preserved verbatim; bodies may still contain the old project path "+
			"(Claude Code reads them by filename for in-session rewinds, not as path references)",
		len(paths),
	))
}
