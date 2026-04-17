// Package move implements project directory move operations for Claude Code projects.
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
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/scan"
)

// Options holds the parameters for a project move operation.
type Options struct {
	OldPath            string
	NewPath            string
	RewriteTranscripts bool
	RefsOnly           bool

	// WarningWriter receives human-readable warnings emitted during Apply
	// (e.g. malformed lines in history.jsonl that the move preserves but
	// cannot repair). Defaults to os.Stderr when nil. DryRun does not use
	// this field — it surfaces warnings through Plan instead.
	WarningWriter io.Writer
}

// Plan holds the results of a dry-run move operation.
type Plan struct {
	OldProjectDir string
	NewProjectDir string

	HistoryReplacements           int
	SessionFileReplacements       int
	SettingsReplacements          int
	ConfigBlockRekey              bool
	TranscriptReplacements        int
	FileHistorySnapshotsPreserved int

	// HistoryMalformedLines is the 1-based line numbers of history.jsonl
	// entries that failed JSON parsing. The move preserves them verbatim;
	// repairing them is out of scope.
	HistoryMalformedLines []int

	RulesWarnings []scan.Warning

	MoveProjectDir bool
}

// DryRun analyses what a move would change without writing any files.
// It locates all project data, counts replacements for each file type,
// and scans rules files for warnings.
func DryRun(claudeHome *claude.Home, moveOptions Options) (*Plan, error) {
	if err := checkEncodedDirCollision(claudeHome, moveOptions.OldPath, moveOptions.NewPath); err != nil {
		return nil, err
	}

	locations, err := claude.LocateProject(claudeHome, moveOptions.OldPath)
	if err != nil {
		return nil, fmt.Errorf("locate project: %w", err)
	}

	plan := &Plan{
		OldProjectDir:  claudeHome.ProjectDir(moveOptions.OldPath),
		NewProjectDir:  claudeHome.ProjectDir(moveOptions.NewPath),
		MoveProjectDir: !moveOptions.RefsOnly,
	}

	plan.HistoryReplacements, plan.HistoryMalformedLines, err = scanHistoryFile(claudeHome, moveOptions)
	if err != nil {
		return nil, err
	}

	plan.SessionFileReplacements, err = countSessionFileReplacements(locations, moveOptions)
	if err != nil {
		return nil, err
	}

	plan.SettingsReplacements, err = countSettingsReplacements(claudeHome, moveOptions)
	if err != nil {
		return nil, err
	}

	plan.ConfigBlockRekey, err = checkConfigBlockRekey(claudeHome, moveOptions)
	if err != nil {
		return nil, err
	}

	if moveOptions.RewriteTranscripts {
		plan.TranscriptReplacements, err = countTranscriptReplacements(locations, moveOptions)
		if err != nil {
			return nil, err
		}
	}

	plan.FileHistorySnapshotsPreserved, err = countFileHistorySnapshots(locations)
	if err != nil {
		return nil, err
	}

	warnings, err := scan.Rules(claudeHome.RulesDir(), moveOptions.OldPath)
	if err != nil {
		return nil, fmt.Errorf("scan rules: %w", err)
	}
	plan.RulesWarnings = warnings

	return plan, nil
}

func scanHistoryFile(claudeHome *claude.Home, moveOptions Options) (int, []int, error) {
	historyFile := claudeHome.HistoryFile()
	if _, err := os.Stat(historyFile); err != nil {
		return 0, nil, nil
	}

	data, err := os.ReadFile(historyFile) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return 0, nil, fmt.Errorf("read history.jsonl: %w", err)
	}
	_, count, malformed, err := rewrite.HistoryJSONL(data, moveOptions.OldPath, moveOptions.NewPath)
	if err != nil {
		return 0, nil, fmt.Errorf("analyse history.jsonl: %w", err)
	}
	return count, malformed, nil
}

func countSessionFileReplacements(locations *claude.ProjectLocations, moveOptions Options) (int, error) {
	count := 0
	for _, sessionFilePath := range locations.SessionFiles {
		data, err := os.ReadFile(sessionFilePath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return 0, fmt.Errorf("read session file %s: %w", sessionFilePath, err)
		}
		_, changed, err := rewrite.SessionFile(data, moveOptions.OldPath, moveOptions.NewPath)
		if err != nil {
			return 0, fmt.Errorf("analyse session file %s: %w", sessionFilePath, err)
		}
		if changed {
			count++
		}
	}
	return count, nil
}

func countSettingsReplacements(claudeHome *claude.Home, moveOptions Options) (int, error) {
	settingsFile := claudeHome.SettingsFile()
	if _, err := os.Stat(settingsFile); err != nil {
		return 0, nil
	}

	data, err := os.ReadFile(settingsFile) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return 0, fmt.Errorf("read settings.json: %w", err)
	}
	_, count := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
	return count, nil
}

func checkConfigBlockRekey(claudeHome *claude.Home, moveOptions Options) (bool, error) {
	if _, err := os.Stat(claudeHome.ConfigFile); err != nil {
		return false, nil
	}

	data, err := os.ReadFile(claudeHome.ConfigFile)
	if err != nil {
		return false, fmt.Errorf("read config file: %w", err)
	}
	_, rekeyed, err := rewrite.UserConfig(data, moveOptions.OldPath, moveOptions.NewPath)
	if err != nil {
		return false, fmt.Errorf("analyse config file: %w", err)
	}
	return rekeyed, nil
}

func countTranscriptReplacements(locations *claude.ProjectLocations, moveOptions Options) (int, error) {
	transcriptPaths, err := listTranscriptFiles(locations.ProjectDir)
	if err != nil {
		return 0, err
	}

	total := 0
	for _, transcriptPath := range transcriptPaths {
		data, err := os.ReadFile(transcriptPath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return 0, fmt.Errorf("read transcript %s: %w", transcriptPath, err)
		}
		_, count := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
		total += count
	}
	return total, nil
}

// countFileHistorySnapshots returns the number of snapshot files under the
// project's file-history directories. The move preserves every snapshot as-is
// — their contents are opaque user-file bytes — so the dry-run plan surfaces
// the count so the user knows how many files will carry over unchanged and
// may still reference the old project path inside their bytes.
func countFileHistorySnapshots(locations *claude.ProjectLocations) (int, error) {
	total := 0
	for _, fileHistoryDir := range locations.FileHistoryDirs {
		snapshotPaths, err := listFilesRecursive(fileHistoryDir)
		if err != nil {
			return 0, fmt.Errorf("walk file-history dir %s: %w", fileHistoryDir, err)
		}
		total += len(snapshotPaths)
	}
	return total, nil
}

// Apply performs the project move. It uses a copy-verify-delete strategy so that
// originals are only removed after all new data is successfully created.
//
// On any failure, all newly created paths are removed and the originals remain
// untouched.
//
// Before any work, Apply acquires an exclusive advisory lock over claudeHome
// and aborts if a Claude Code session is currently live or if another
// cc-port invocation is already operating on the same directory.
func Apply(claudeHome *claude.Home, moveOptions Options) error {
	lockHandle, err := lock.Acquire(claudeHome)
	if err != nil {
		return err
	}
	defer func() { _ = lockHandle.Release() }()

	if err := checkEncodedDirCollision(claudeHome, moveOptions.OldPath, moveOptions.NewPath); err != nil {
		return err
	}

	locations, err := claude.LocateProject(claudeHome, moveOptions.OldPath)
	if err != nil {
		return fmt.Errorf("locate project: %w", err)
	}

	oldProjectDir := claudeHome.ProjectDir(moveOptions.OldPath)
	newProjectDir := claudeHome.ProjectDir(moveOptions.NewPath)
	return executeMove(claudeHome, locations, oldProjectDir, newProjectDir, moveOptions)
}

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
		tracker.restore()
		return err
	}

	if !moveOptions.RefsOnly {
		createdPaths = append(createdPaths, moveOptions.NewPath)
		if err := fsutil.CopyDir(moveOptions.OldPath, moveOptions.NewPath); err != nil {
			tracker.restore()
			return fmt.Errorf("copy project on disk: %w", err)
		}
	}

	if err := verifyNewDirs(newProjectDir, moveOptions); err != nil {
		tracker.restore()
		return err
	}

	if err := deleteOriginals(oldProjectDir, moveOptions, tracker); err != nil {
		return err
	}

	warnFileHistoryPreserved(locations, moveOptions)

	success = true
	return nil
}

// rewriteNewProjectDir rewrites the copied project dir: transcripts and memory files.
func rewriteNewProjectDir(newProjectDir string, moveOptions Options) error {
	if moveOptions.RewriteTranscripts {
		if err := rewriteTranscriptsInDir(newProjectDir, moveOptions); err != nil {
			return err
		}
	}

	if err := rewriteMemoryFilesInDir(newProjectDir, moveOptions); err != nil {
		return err
	}

	return nil
}

func rewriteTranscriptsInDir(newProjectDir string, moveOptions Options) error {
	newTranscripts, err := listTranscriptFiles(newProjectDir)
	if err != nil {
		return fmt.Errorf("collect transcripts in new dir: %w", err)
	}
	for _, transcriptPath := range newTranscripts {
		data, err := os.ReadFile(transcriptPath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return fmt.Errorf("read transcript %s: %w", transcriptPath, err)
		}
		rewritten, _ := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
		if err := rewrite.SafeWriteFile(transcriptPath, rewritten, 0644); err != nil {
			return fmt.Errorf("write transcript %s: %w", transcriptPath, err)
		}
	}
	return nil
}

func rewriteMemoryFilesInDir(newProjectDir string, moveOptions Options) error {
	newMemoryDir := filepath.Join(newProjectDir, "memory")
	if _, err := os.Stat(newMemoryDir); err != nil {
		return nil
	}

	memoryEntries, err := os.ReadDir(newMemoryDir)
	if err != nil {
		return fmt.Errorf("read new memory directory: %w", err)
	}
	for _, entry := range memoryEntries {
		if entry.IsDir() {
			continue
		}
		memoryFilePath := filepath.Join(newMemoryDir, entry.Name())
		data, err := os.ReadFile(memoryFilePath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return fmt.Errorf("read memory file %s: %w", memoryFilePath, err)
		}
		rewritten, _ := rewrite.ReplacePathInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
		if err := rewrite.SafeWriteFile(memoryFilePath, rewritten, 0644); err != nil {
			return fmt.Errorf("write memory file %s: %w", memoryFilePath, err)
		}
	}
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

func (t *globalFileTracker) restore() {
	for _, s := range t.saved {
		_ = rewrite.SafeWriteFile(s.path, s.data, s.mode)
	}
}

// rewriteGlobalFiles rewrites history.jsonl, session files, settings.json, and
// the user config file in place, saving originals to the tracker for rollback.
func rewriteGlobalFiles(
	claudeHome *claude.Home,
	locations *claude.ProjectLocations,
	moveOptions Options,
	tracker *globalFileTracker,
) error {
	if err := rewriteHistoryFile(claudeHome, moveOptions, tracker); err != nil {
		return err
	}
	if err := rewriteSessionFiles(locations, moveOptions, tracker); err != nil {
		return err
	}
	if err := rewriteSettingsFile(claudeHome, moveOptions, tracker); err != nil {
		return err
	}
	if err := rewriteConfigFile(claudeHome, moveOptions, tracker); err != nil {
		return err
	}
	return nil
}

func rewriteHistoryFile(claudeHome *claude.Home, moveOptions Options, tracker *globalFileTracker) error {
	historyFile := claudeHome.HistoryFile()
	if _, err := os.Stat(historyFile); err != nil {
		return nil
	}

	original, mode, err := tracker.save(historyFile)
	if err != nil {
		return fmt.Errorf("read history.jsonl for backup: %w", err)
	}
	rewritten, _, malformed, err := rewrite.HistoryJSONL(original, moveOptions.OldPath, moveOptions.NewPath)
	if err != nil {
		return fmt.Errorf("rewrite history.jsonl: %w", err)
	}
	if err := rewrite.SafeWriteFile(historyFile, rewritten, mode); err != nil {
		return fmt.Errorf("write history.jsonl: %w", err)
	}
	if len(malformed) > 0 {
		_, _ = fmt.Fprintf(
			warningWriter(moveOptions),
			"warning: history.jsonl contains %d malformed line(s) at %v — preserved verbatim, not rewritten\n",
			len(malformed), malformed,
		)
	}
	return nil
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
		snapshotPaths, err := listFilesRecursive(fileHistoryDir)
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

func rewriteSessionFiles(
	locations *claude.ProjectLocations,
	moveOptions Options,
	tracker *globalFileTracker,
) error {
	for _, sessionFilePath := range locations.SessionFiles {
		original, mode, err := tracker.save(sessionFilePath)
		if err != nil {
			return fmt.Errorf("read session file %s for backup: %w", sessionFilePath, err)
		}
		rewritten, _, err := rewrite.SessionFile(original, moveOptions.OldPath, moveOptions.NewPath)
		if err != nil {
			return fmt.Errorf("rewrite session file %s: %w", sessionFilePath, err)
		}
		if err := rewrite.SafeWriteFile(sessionFilePath, rewritten, mode); err != nil {
			return fmt.Errorf("write session file %s: %w", sessionFilePath, err)
		}
	}
	return nil
}

func rewriteSettingsFile(claudeHome *claude.Home, moveOptions Options, tracker *globalFileTracker) error {
	settingsFile := claudeHome.SettingsFile()
	if _, err := os.Stat(settingsFile); err != nil {
		return nil
	}

	original, mode, err := tracker.save(settingsFile)
	if err != nil {
		return fmt.Errorf("read settings.json for backup: %w", err)
	}
	rewritten, _ := rewrite.ReplacePathInBytes(original, moveOptions.OldPath, moveOptions.NewPath)
	if err := rewrite.SafeWriteFile(settingsFile, rewritten, mode); err != nil {
		return fmt.Errorf("write settings.json: %w", err)
	}
	return nil
}

func rewriteConfigFile(claudeHome *claude.Home, moveOptions Options, tracker *globalFileTracker) error {
	configFile := claudeHome.ConfigFile
	if _, err := os.Stat(configFile); err != nil {
		return nil
	}

	original, mode, err := tracker.save(configFile)
	if err != nil {
		return fmt.Errorf("read config file for backup: %w", err)
	}
	rewritten, _, err := rewrite.UserConfig(original, moveOptions.OldPath, moveOptions.NewPath)
	if err != nil {
		return fmt.Errorf("rewrite config file: %w", err)
	}
	if err := rewrite.SafeWriteFile(configFile, rewritten, mode); err != nil {
		return fmt.Errorf("write config file: %w", err)
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
		tracker.restore()
		return fmt.Errorf("remove old project data dir: %w", err)
	}
	if !moveOptions.RefsOnly {
		if err := os.RemoveAll(moveOptions.OldPath); err != nil {
			return fmt.Errorf("remove old project dir on disk: %w", err)
		}
	}
	return nil
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
		subdirFiles, err := listFilesRecursive(fullPath)
		if err != nil {
			return nil, err
		}
		transcripts = append(transcripts, subdirFiles...)
	}
	return transcripts, nil
}

// listFilesRecursive returns every file path under dir, skipping directories.
func listFilesRecursive(dir string) ([]string, error) {
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
