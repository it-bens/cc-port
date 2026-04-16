// Package move implements project directory move operations for Claude Code projects.
package move

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/fsutil"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/scan"
)

// Options holds the parameters for a project move operation.
type Options struct {
	OldPath            string
	NewPath            string
	RewriteTranscripts bool
	RefsOnly           bool
}

// Plan holds the results of a dry-run move operation.
type Plan struct {
	OldProjectDir string
	NewProjectDir string

	SessionsIndexReplacements int
	HistoryReplacements       int
	SessionFileReplacements   int
	SettingsReplacements      int
	ConfigBlockRekey          bool
	TranscriptReplacements    int

	RulesWarnings []scan.Warning

	MoveProjectDir bool
}

// DryRun analyses what a move would change without writing any files.
// It locates all project data, counts replacements for each file type,
// and scans rules files for warnings.
func DryRun(claudeHome *claude.Home, moveOptions Options) (*Plan, error) {
	locations, err := claude.LocateProject(claudeHome, moveOptions.OldPath)
	if err != nil {
		return nil, fmt.Errorf("locate project: %w", err)
	}

	plan := &Plan{
		OldProjectDir:  claudeHome.ProjectDir(moveOptions.OldPath),
		NewProjectDir:  claudeHome.ProjectDir(moveOptions.NewPath),
		MoveProjectDir: !moveOptions.RefsOnly,
	}

	plan.SessionsIndexReplacements, err = countSessionsIndexReplacements(locations, claudeHome, moveOptions)
	if err != nil {
		return nil, err
	}

	plan.HistoryReplacements, err = countHistoryReplacements(claudeHome, moveOptions)
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

	warnings, err := scan.Rules(claudeHome.RulesDir(), moveOptions.OldPath)
	if err != nil {
		return nil, fmt.Errorf("scan rules: %w", err)
	}
	plan.RulesWarnings = warnings

	return plan, nil
}

func countSessionsIndexReplacements(
	locations *claude.ProjectLocations,
	claudeHome *claude.Home,
	moveOptions Options,
) (int, error) {
	if locations.SessionsIndex == "" {
		return 0, nil
	}

	data, err := os.ReadFile(locations.SessionsIndex)
	if err != nil {
		return 0, fmt.Errorf("read sessions-index.json: %w", err)
	}
	_, count, err := rewrite.SessionsIndex(
		data,
		moveOptions.OldPath,
		moveOptions.NewPath,
		claudeHome.ProjectDir(moveOptions.OldPath),
		claudeHome.ProjectDir(moveOptions.NewPath),
	)
	if err != nil {
		return 0, fmt.Errorf("analyse sessions-index.json: %w", err)
	}
	return count, nil
}

func countHistoryReplacements(claudeHome *claude.Home, moveOptions Options) (int, error) {
	historyFile := claudeHome.HistoryFile()
	if _, err := os.Stat(historyFile); err != nil {
		return 0, nil
	}

	data, err := os.ReadFile(historyFile) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return 0, fmt.Errorf("read history.jsonl: %w", err)
	}
	_, count, err := rewrite.HistoryJSONL(data, moveOptions.OldPath, moveOptions.NewPath)
	if err != nil {
		return 0, fmt.Errorf("analyse history.jsonl: %w", err)
	}
	return count, nil
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
	_, count := rewrite.ReplaceInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
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
	total := 0
	for _, transcriptPath := range locations.SessionTranscripts {
		data, err := os.ReadFile(transcriptPath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return 0, fmt.Errorf("read transcript %s: %w", transcriptPath, err)
		}
		total += bytes.Count(data, []byte(moveOptions.OldPath))
	}
	return total, nil
}

// Apply performs the project move. It uses a copy-verify-delete strategy so that
// originals are only removed after all new data is successfully created.
//
// On any failure, all newly created paths are removed and the originals remain
// untouched.
func Apply(claudeHome *claude.Home, moveOptions Options) error {
	locations, err := claude.LocateProject(claudeHome, moveOptions.OldPath)
	if err != nil {
		return fmt.Errorf("locate project: %w", err)
	}

	oldProjectDir := claudeHome.ProjectDir(moveOptions.OldPath)
	newProjectDir := claudeHome.ProjectDir(moveOptions.NewPath)

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

	if err := rewriteNewProjectDir(newProjectDir, oldProjectDir, moveOptions); err != nil {
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

	success = true
	return nil
}

// rewriteNewProjectDir rewrites the copied project dir: sessions-index, transcripts, memory files.
func rewriteNewProjectDir(newProjectDir, oldProjectDir string, moveOptions Options) error {
	newSessionsIndex := filepath.Join(newProjectDir, "sessions-index.json")
	if _, err := os.Stat(newSessionsIndex); err == nil {
		if err := rewriteSessionsIndexFile(newSessionsIndex, oldProjectDir, newProjectDir, moveOptions); err != nil {
			return err
		}
	}

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

func rewriteSessionsIndexFile(path, oldProjectDir, newProjectDir string, moveOptions Options) error {
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from trusted internal data
	if err != nil {
		return fmt.Errorf("read new sessions-index.json: %w", err)
	}
	rewritten, _, err := rewrite.SessionsIndex(
		data,
		moveOptions.OldPath,
		moveOptions.NewPath,
		oldProjectDir,
		newProjectDir,
	)
	if err != nil {
		return fmt.Errorf("rewrite sessions-index.json: %w", err)
	}
	if err := rewrite.SafeWriteFile(path, rewritten, 0644); err != nil {
		return fmt.Errorf("write new sessions-index.json: %w", err)
	}
	return nil
}

func rewriteTranscriptsInDir(newProjectDir string, moveOptions Options) error {
	newTranscripts, err := collectNewDirTranscripts(newProjectDir)
	if err != nil {
		return fmt.Errorf("collect transcripts in new dir: %w", err)
	}
	for _, transcriptPath := range newTranscripts {
		data, err := os.ReadFile(transcriptPath) //nolint:gosec // path constructed from trusted internal data
		if err != nil {
			return fmt.Errorf("read transcript %s: %w", transcriptPath, err)
		}
		rewritten, _ := rewrite.ReplaceInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
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
		rewritten, _ := rewrite.ReplaceInBytes(data, moveOptions.OldPath, moveOptions.NewPath)
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
	rewritten, _, err := rewrite.HistoryJSONL(original, moveOptions.OldPath, moveOptions.NewPath)
	if err != nil {
		return fmt.Errorf("rewrite history.jsonl: %w", err)
	}
	if err := rewrite.SafeWriteFile(historyFile, rewritten, mode); err != nil {
		return fmt.Errorf("write history.jsonl: %w", err)
	}
	return nil
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
	rewritten, _ := rewrite.ReplaceInBytes(original, moveOptions.OldPath, moveOptions.NewPath)
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

// collectNewDirTranscripts returns paths to all .jsonl files directly in the
// given project directory (not recursively).
func collectNewDirTranscripts(projectDir string) ([]string, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	var transcripts []string
	for _, entry := range entries {
		if !entry.IsDir() && len(entry.Name()) > 6 && entry.Name()[len(entry.Name())-6:] == ".jsonl" {
			transcripts = append(transcripts, filepath.Join(projectDir, entry.Name()))
		}
	}
	return transcripts, nil
}
