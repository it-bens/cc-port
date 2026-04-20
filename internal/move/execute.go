package move

import (
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
	claudeHome *claude.Home,
	locations *claude.ProjectLocations,
	oldProjectDir, newProjectDir string,
	moveOptions Options,
) error {
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
	if err := fsutil.CopyDir(oldProjectDir, newProjectDir); err != nil {
		return fmt.Errorf("copy project directory: %w", err)
	}

	tracker := &globalFileTracker{}

	if err := rewriteNewProjectDir(newProjectDir, moveOptions); err != nil {
		return err
	}

	if err := rewriteGlobalFiles(claudeHome, locations, moveOptions, tracker); err != nil {
		return errors.Join(err, tracker.restore())
	}

	if !moveOptions.RefsOnly {
		createdPaths = append(createdPaths, moveOptions.NewPath)
		if err := fsutil.CopyDir(moveOptions.OldPath, moveOptions.NewPath); err != nil {
			return errors.Join(fmt.Errorf("copy project on disk: %w", err), tracker.restore())
		}
	}

	if err := verifyNewDirs(newProjectDir, moveOptions); err != nil {
		return errors.Join(err, tracker.restore())
	}

	if err := deleteOriginals(oldProjectDir, moveOptions, tracker); err != nil {
		return err
	}

	warnFileHistoryPreserved(locations, moveOptions)

	success = true
	return nil
}

// globalFileTracker records the original contents of global files so they can
// be restored if Apply fails partway through.
type globalFileTracker struct {
	saved []savedFile
}

type savedFile struct {
	path string
	data []byte
	mode os.FileMode
}

func (t *globalFileTracker) save(path string) ([]byte, os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return nil, 0, err
	}
	t.saved = append(t.saved, savedFile{path, data, info.Mode()})
	return data, info.Mode(), nil
}

func (t *globalFileTracker) restore() error {
	var errs []error
	for _, s := range t.saved {
		if err := rewrite.SafeWriteFile(s.path, s.data, s.mode); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", s.path, err))
		}
	}
	return errors.Join(errs...)
}

// checkEncodedDirCollision refuses moves that would overwrite existing encoded
// storage or collapse old and new onto the same directory. See internal/claude
// README §Path encoding for the lossy encoding the check defends against.
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
			// Encoded dir is already gone; we cannot resurrect it, but restoring
			// globals keeps them consistent with the old path so the user can
			// investigate or rerun instead of pointing at a deleted location.
			return errors.Join(fmt.Errorf("remove old project dir on disk: %w", err), tracker.restore())
		}
	}
	return nil
}

// listTranscriptFiles returns every file under projectDir that
// RewriteTranscripts should rewrite: top-level `.jsonl` files, plus every file
// under each session subdirectory (covering <uuid>/subagents/*.jsonl and
// <uuid>/session-memory/**).
//
// `memory/` is handled separately by rewriteMemoryFilesInDir, so it is
// excluded here.
func listTranscriptFiles(projectDir string) ([]string, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("read project directory: %w", err)
	}

	var transcripts []string
	for _, entry := range entries {
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
		subdirFiles, err := ListFilesRecursive(fullPath)
		if err != nil {
			return nil, err
		}
		transcripts = append(transcripts, subdirFiles...)
	}
	return transcripts, nil
}

// ListFilesRecursive returns every file path under dir, skipping directories.
func ListFilesRecursive(dir string) ([]string, error) {
	var files []string
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
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

// warningWriter returns the writer to which Apply sends human-readable
// warnings. It defaults to os.Stderr so unconfigured callers still see
// warnings; tests inject a buffer via Options.WarningWriter.
func warningWriter(moveOptions Options) io.Writer {
	if moveOptions.WarningWriter != nil {
		return moveOptions.WarningWriter
	}
	return os.Stderr
}

// warnFileHistoryPreserved emits a single warning line per move when the
// project has any file-history snapshots. cc-port never rewrites snapshot
// contents — they are verbatim copies of user-edited files whose project-
// path strings are coincidental — and the warning surfaces that invariant
// so the user is not surprised when a grep across ~/.claude/file-history/
// still returns the old project path after a move.
func warnFileHistoryPreserved(locations *claude.ProjectLocations, moveOptions Options) {
	count := 0
	for _, fileHistoryDir := range locations.FileHistoryDirs {
		snapshotPaths, err := ListFilesRecursive(fileHistoryDir)
		if err != nil {
			// The same walk already succeeded during rewriteGlobalFiles'
			// preceding work; an error here is unexpected. Skip the warning
			// rather than fail the apply — the move itself is complete.
			return
		}
		count += len(snapshotPaths)
	}
	if count == 0 {
		return
	}
	_, _ = fmt.Fprintf(
		warningWriter(moveOptions),
		"warning: %d file-history snapshot(s) preserved as-is — contents may still reference the old project path "+
			"(used for in-session rewinds, not persisted data)\n",
		count,
	)
}
