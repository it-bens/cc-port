// Package importer handles importing cc-port ZIP archives into a Claude Code home directory.
package importer

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/transport"
)

// dirPerm — 0755, so group/others can traverse project subdirs shared with tooling.
const dirPerm = os.FileMode(0755)

// filePerm is the mode used for files written during import.
// rw-r--r-- — owner read/write, group and others read-only, matching the
// permissions Claude Code itself writes for project data files.
const filePerm = os.FileMode(0644)

// stagingSuffix is appended to every final destination to form its temp path.
// Import writes to temp paths first, then atomically promotes them via
// SafeRenamePromoter. The suffix is distinctive enough to survive casual
// filesystem inspection if a crash ever leaves one behind.
const stagingSuffix = ".cc-port-import.tmp"

// stagingTempPath returns the temp path used to stage finalPath before
// atomic promotion. The temp is formed inside the symlink-resolved parent
// of finalPath so that temp and final always live on the same filesystem,
// which os.Rename requires. Without this, a symlinked parent pointing at
// another volume (e.g. ~/.claude/file-history -> /Volumes/ext/...) would
// place the sibling temp on one side of the boundary and the rename
// target on the other, and the promote step would fail with EXDEV.
func stagingTempPath(finalPath string) (string, error) {
	resolvedParent, err := resolveExistingAncestor(filepath.Dir(finalPath))
	if err != nil {
		return "", fmt.Errorf("resolve staging parent for %q: %w", finalPath, err)
	}
	return filepath.Join(resolvedParent, filepath.Base(finalPath)+stagingSuffix), nil
}

// resolveExistingAncestor walks dir upward to the longest prefix that
// exists on disk, evaluates symlinks on that prefix, and re-attaches any
// missing trailing components unchanged. This mirrors the behaviour of
// claude.ResolveProjectPath but operates on arbitrary directory paths —
// notably the parents of destinations like ~/.claude/history.jsonl whose
// final leaf does not yet exist at preflight time.
func resolveExistingAncestor(dir string) (string, error) {
	existingPrefix := dir
	var missingSuffix string
	for {
		if _, err := os.Lstat(existingPrefix); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat %q: %w", existingPrefix, err)
		}
		if existingPrefix == "/" || existingPrefix == "." {
			break
		}
		parent, child := filepath.Split(existingPrefix)
		existingPrefix = strings.TrimSuffix(parent, "/")
		if existingPrefix == "" {
			existingPrefix = "/"
		}
		if missingSuffix == "" {
			missingSuffix = child
		} else {
			missingSuffix = filepath.Join(child, missingSuffix)
		}
	}

	resolvedPrefix, err := filepath.EvalSymlinks(existingPrefix)
	if err != nil {
		return "", fmt.Errorf("evaluate symlinks for %q: %w", existingPrefix, err)
	}
	if missingSuffix == "" {
		return resolvedPrefix, nil
	}
	return filepath.Join(resolvedPrefix, missingSuffix), nil
}

// checkStagingFilesystems resolves the parent directory of every final
// destination up front. Any resolution error is surfaced as a single
// aggregate error before the archive is read or any temp is created, so
// users whose ~/.claude layout contains a broken symlink see one clear
// message instead of a rename failure mid-promote.
//
// File-history snapshot destinations are represented by their shared base
// directory; per-snapshot parents created inside that base (one per
// session UUID) are resolved at stage time via stagingTempPath.
func checkStagingFilesystems(claudeHome *claude.Home, encodedProjectDir string) error {
	destinations := []string{
		encodedProjectDir,
		claudeHome.HistoryFile(),
		claudeHome.ConfigFile,
		filepath.Join(claudeHome.FileHistoryDir(), "placeholder"),
		filepath.Join(claudeHome.TodosDir(), "placeholder"),
		filepath.Join(claudeHome.UsageDataDir(), "session-meta", "placeholder"),
		filepath.Join(claudeHome.UsageDataDir(), "facets", "placeholder"),
		filepath.Join(claudeHome.PluginsDataDir(), "placeholder", "placeholder", "placeholder"),
		filepath.Join(claudeHome.TasksDir(), "placeholder", "placeholder"),
	}
	var errs []string
	for _, dest := range destinations {
		if _, err := stagingTempPath(dest); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("staging filesystem check: %s", strings.Join(errs, "; "))
}

// Options configures an import operation.
type Options struct {
	ArchivePath string
	TargetPath  string
	Resolutions map[string]string

	// renameHook lets tests inject promote-time failures. When nil, Run uses
	// os.Rename directly via SafeRenamePromoter. Package-internal by design.
	renameHook func(oldpath, newpath string) error
}

// archiveEntry is one decoded non-metadata ZIP entry, holding the raw body
// content after placeholder resolution has been applied.
type archiveEntry struct {
	name    string
	content []byte
}

// Run imports a cc-port ZIP archive into claudeHome. The pipeline is:
//
//  1. Acquire the claudeHome lock and perform conflict/TTY preflight.
//  2. Read every non-metadata ZIP entry into memory.
//  3. Resolve the manifest's placeholder classification against the caller's
//     resolutions; refuse before any write if the archive would leave
//     unresolved or undeclared tokens on disk.
//  4. Stage every final destination at a *.cc-port-import.tmp path inside
//     the symlink-resolved parent of the destination (so temp and final
//     always share a filesystem).
//  5. Promote all staged temps atomically via SafeRenamePromoter; on any
//     promote failure, the promoter rolls back every already-promoted entry
//     to its pre-import state.
func Run(claudeHome *claude.Home, importOptions Options) error {
	lockHandle, err := lock.Acquire(claudeHome)
	if err != nil {
		return err
	}
	defer func() { _ = lockHandle.Release() }()

	if err := ValidateResolutions(importOptions.Resolutions); err != nil {
		return fmt.Errorf("validate resolutions: %w", err)
	}

	encodedProjectDir := claudeHome.ProjectDir(importOptions.TargetPath)
	if err := CheckConflict(encodedProjectDir); err != nil {
		return fmt.Errorf("conflict check: %w", err)
	}

	if err := checkStagingFilesystems(claudeHome, encodedProjectDir); err != nil {
		return err
	}

	entries, metadata, err := loadArchive(importOptions.ArchivePath)
	if err != nil {
		return err
	}

	if _, err := manifest.ApplyCategoryEntries(metadata.Export.Categories); err != nil {
		return fmt.Errorf("manifest categories: %w", err)
	}

	resolutions := withProjectPath(importOptions.Resolutions, importOptions.TargetPath)

	if err := runPreflight(entries, metadata, resolutions); err != nil {
		return err
	}

	resolveEntryContents(entries, resolutions)

	plan, err := buildImportPlan(claudeHome, importOptions.TargetPath, encodedProjectDir, entries)
	if err != nil {
		// Clean up whatever temp paths the plan managed to create before the
		// error. buildImportPlan always returns a non-nil plan (including on
		// early failures), but guard explicitly so static analysis is happy.
		if plan != nil {
			_ = plan.cleanupTemps()
		}
		return err
	}

	return promotePlan(plan, importOptions.renameHook)
}

func loadArchive(archivePath string) ([]archiveEntry, *manifest.Metadata, error) {
	zipReader, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = zipReader.Close() }()

	metadata, err := manifest.ReadManifestFromZip(archivePath)
	if err != nil {
		return nil, nil, fmt.Errorf("read metadata from archive: %w", err)
	}

	var entries []archiveEntry
	for _, zipFile := range zipReader.File {
		if zipFile.Name == "metadata.xml" {
			continue
		}
		content, err := readZipFile(zipFile)
		if err != nil {
			return nil, nil, fmt.Errorf("read zip entry %q: %w", zipFile.Name, err)
		}
		entries = append(entries, archiveEntry{name: zipFile.Name, content: content})
	}

	return entries, metadata, nil
}

// withProjectPath returns a copy of resolutions that always contains an
// entry for {{PROJECT_PATH}}, injecting targetPath when the caller did not
// supply one explicitly. The original map is not mutated.
func withProjectPath(resolutions map[string]string, targetPath string) map[string]string {
	result := make(map[string]string, len(resolutions)+1)
	for key, value := range resolutions {
		result[key] = value
	}
	if _, hasProjectPath := result[projectPathKey]; !hasProjectPath {
		result[projectPathKey] = targetPath
	}
	return result
}

// runPreflight fails the import if any placeholder token present in the
// archive bodies is either declared-but-unresolved or present-but-undeclared.
// No write has occurred at this point — aborting here leaves the destination
// untouched.
func runPreflight(entries []archiveEntry, metadata *manifest.Metadata, resolutions map[string]string) error {
	bodies := make([][]byte, len(entries))
	for index, entry := range entries {
		bodies[index] = entry.content
	}
	missing, undeclared := ClassifyPlaceholders(bodies, metadata.Placeholders, resolutions)
	if len(missing) == 0 && len(undeclared) == 0 {
		return nil
	}

	var parts []string
	if len(missing) > 0 {
		parts = append(parts, fmt.Sprintf(
			"missing resolutions for declared placeholder(s) %s", strings.Join(missing, ", "),
		))
	}
	if len(undeclared) > 0 {
		parts = append(parts, fmt.Sprintf(
			"archive contains undeclared placeholder(s) %s", strings.Join(undeclared, ", "),
		))
	}
	return fmt.Errorf("archive preflight: %s", strings.Join(parts, "; "))
}

// resolveEntryContents applies ResolvePlaceholders to each archive entry in
// place. Separated from stage so that the pre-flight gate operates on the
// raw archive bytes (where the placeholder tokens are visible for
// classification).
func resolveEntryContents(entries []archiveEntry, resolutions map[string]string) {
	for index := range entries {
		entries[index].content = ResolvePlaceholders(entries[index].content, resolutions)
	}
}

// importPlan records every staged artifact and the final destination it
// will be promoted onto. The fields are populated by buildImportPlan and
// consumed by promotePlan.
type importPlan struct {
	encodedProjectDir string
	tempProjectDir    string
	projectDirCreated bool

	historyFile     string
	tempHistoryFile string
	historyStaged   bool

	configFile     string
	tempConfigFile string
	configStaged   bool

	fileHistoryFiles        []stagedFile
	sessionKeyedStagedFiles []stagedFile
}

// stagedFile is one artifact staged for atomic promotion onto its final path.
type stagedFile struct {
	group     string
	finalPath string
	tempPath  string
}

// cleanupTemps best-effort removes every temp artifact the plan created.
// Used when buildImportPlan itself fails partway. Any accumulated removal
// errors are joined and returned so callers can log a diagnostic; the
// caller is expected to discard the aggregate since the enclosing path has
// already failed.
func (plan *importPlan) cleanupTemps() error {
	if plan == nil {
		return nil
	}
	var errs []error
	if plan.projectDirCreated {
		if err := os.RemoveAll(plan.tempProjectDir); err != nil {
			errs = append(errs, err)
		}
	}
	if plan.historyStaged {
		if err := os.Remove(plan.tempHistoryFile); err != nil {
			errs = append(errs, err)
		}
	}
	if plan.configStaged {
		if err := os.Remove(plan.tempConfigFile); err != nil {
			errs = append(errs, err)
		}
	}
	for _, entry := range plan.fileHistoryFiles {
		if err := os.Remove(entry.tempPath); err != nil {
			errs = append(errs, err)
		}
	}
	for _, entry := range plan.sessionKeyedStagedFiles {
		if err := os.Remove(entry.tempPath); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// newImportPlan computes the temp-path for every destination the importer
// will touch and returns an empty plan with those paths filled in. The
// temp paths come from stagingTempPath so temp and final always share a
// filesystem even when a parent directory is a symlink crossing a
// filesystem boundary.
func newImportPlan(claudeHome *claude.Home, encodedProjectDir string) (*importPlan, error) {
	tempProjectDir, err := stagingTempPath(encodedProjectDir)
	if err != nil {
		return nil, err
	}
	tempHistoryFile, err := stagingTempPath(claudeHome.HistoryFile())
	if err != nil {
		return nil, err
	}
	tempConfigFile, err := stagingTempPath(claudeHome.ConfigFile)
	if err != nil {
		return nil, err
	}
	return &importPlan{
		encodedProjectDir: encodedProjectDir,
		tempProjectDir:    tempProjectDir,
		historyFile:       claudeHome.HistoryFile(),
		tempHistoryFile:   tempHistoryFile,
		configFile:        claudeHome.ConfigFile,
		tempConfigFile:    tempConfigFile,
	}, nil
}

// stageArchiveEntries routes each entry to its staging helper, populating the
// plan's slice fields in place. It returns the accumulated history chunks and
// the raw config block so buildImportPlan can post-process them after all
// entries have been staged.
func stageArchiveEntries(
	claudeHome *claude.Home,
	plan *importPlan,
	entries []archiveEntry,
) (historyAppends [][]byte, configBlock []byte, err error) {
	for _, entry := range entries {
		if handled, err := dispatchSessionKeyed(claudeHome, plan, entry); err != nil {
			return nil, nil, err
		} else if handled {
			continue
		}
		switch {
		case strings.HasPrefix(entry.name, "sessions/"):
			if err := stageProjectFile(plan.tempProjectDir, entry.name, "sessions/", entry.content); err != nil {
				return nil, nil, err
			}
		case strings.HasPrefix(entry.name, "memory/"):
			if err := stageMemoryFile(plan.tempProjectDir, entry.name, entry.content); err != nil {
				return nil, nil, err
			}
		case entry.name == "history/history.jsonl":
			historyAppends = append(historyAppends, entry.content)
		case strings.HasPrefix(entry.name, "file-history/"):
			staged, err := stageFileHistory(claudeHome.FileHistoryDir(), entry.name, entry.content)
			if err != nil {
				return nil, nil, err
			}
			plan.fileHistoryFiles = append(plan.fileHistoryFiles, staged)
		case entry.name == "config.json":
			configBlock = entry.content
		default:
			return nil, nil, fmt.Errorf("unknown archive entry: %q", entry.name)
		}
	}
	return historyAppends, configBlock, nil
}

// dispatchSessionKeyed returns (true, nil) if the entry matched a session-keyed
// ZipPrefix and was staged successfully; (true, err) if it matched but failed
// to stage; (false, nil) if no prefix matched. First prefix match wins.
func dispatchSessionKeyed(claudeHome *claude.Home, plan *importPlan, entry archiveEntry) (bool, error) {
	for _, target := range transport.SessionKeyedTargets {
		if !strings.HasPrefix(entry.name, target.ZipPrefix) {
			continue
		}
		staged, err := stageSessionKeyedFile(claudeHome, target, entry.name, entry.content)
		if err != nil {
			return true, err
		}
		plan.sessionKeyedStagedFiles = append(plan.sessionKeyedStagedFiles, staged)
		return true, nil
	}
	return false, nil
}

// buildImportPlan routes each archive entry to its staged temp destination
// and writes it there. Caller must either call promotePlan (success path)
// or plan.cleanupTemps (failure path).
func buildImportPlan(
	claudeHome *claude.Home,
	targetPath string,
	encodedProjectDir string,
	entries []archiveEntry,
) (*importPlan, error) {
	plan, err := newImportPlan(claudeHome, encodedProjectDir)
	if err != nil {
		return nil, err
	}

	if err := ensureEmptyDir(plan.tempProjectDir); err != nil {
		return plan, fmt.Errorf("stage project directory: %w", err)
	}
	plan.projectDirCreated = true

	historyAppends, configBlock, err := stageArchiveEntries(claudeHome, plan, entries)
	if err != nil {
		return plan, err
	}

	if err := stageHistoryIfNeeded(plan, historyAppends); err != nil {
		return plan, err
	}
	if err := stageConfigIfNeeded(plan, targetPath, configBlock); err != nil {
		return plan, err
	}
	return plan, nil
}

func stageProjectFile(tempProjectDir, zipName, zipPrefix string, content []byte) error {
	relativePath := strings.TrimPrefix(zipName, zipPrefix)
	destinationPath := filepath.Join(tempProjectDir, relativePath)
	return writeStagedFile(destinationPath, content)
}

func stageMemoryFile(tempProjectDir, zipName string, content []byte) error {
	relativePath := strings.TrimPrefix(zipName, "memory/")
	destinationPath := filepath.Join(tempProjectDir, "memory", relativePath)
	return writeStagedFile(destinationPath, content)
}

// stageFileHistory writes a file-history/<uuid>/<hash>@vN entry to a sibling
// temp path of its final destination. It returns the staged paths so the
// promoter can register the rename.
func stageFileHistory(fileHistoryBaseDir, zipName string, content []byte) (stagedFile, error) {
	relativePath := strings.TrimPrefix(zipName, "file-history/")
	finalPath := filepath.Join(fileHistoryBaseDir, relativePath)
	tempPath, err := stagingTempPath(finalPath)
	if err != nil {
		return stagedFile{}, err
	}
	if err := writeStagedFile(tempPath, content); err != nil {
		return stagedFile{}, err
	}
	return stagedFile{finalPath: finalPath, tempPath: tempPath}, nil
}

// stageSessionKeyedFile stages one session-keyed archive entry to a sibling
// temp path under the target group's home base directory. The returned
// stagedFile is tagged with the target's Group name so downstream diagnostics
// can attribute the entry to its originating registry row.
func stageSessionKeyedFile(
	claudeHome *claude.Home, target transport.SessionKeyedTarget, zipName string, content []byte,
) (stagedFile, error) {
	relative := strings.TrimPrefix(zipName, target.ZipPrefix)
	finalPath := filepath.Join(target.HomeBaseDir(claudeHome), relative)
	tempPath, err := stagingTempPath(finalPath)
	if err != nil {
		return stagedFile{}, err
	}
	if err := writeStagedFile(tempPath, content); err != nil {
		return stagedFile{}, err
	}
	return stagedFile{group: target.Group, finalPath: finalPath, tempPath: tempPath}, nil
}

// stageHistoryIfNeeded writes a merged history file to plan.tempHistoryFile
// when the archive supplied any history content.
func stageHistoryIfNeeded(plan *importPlan, appends [][]byte) error {
	if len(appends) == 0 {
		return nil
	}

	existing, err := readExistingOrEmpty(plan.historyFile)
	if err != nil {
		return fmt.Errorf("read existing history for merge: %w", err)
	}
	merged := buildHistoryBytes(existing, appends)
	if err := writeStagedFile(plan.tempHistoryFile, merged); err != nil {
		return err
	}
	plan.historyStaged = true
	return nil
}

// stageConfigIfNeeded writes a merged config file to plan.tempConfigFile
// when the archive supplied a config.json entry.
func stageConfigIfNeeded(plan *importPlan, targetPath string, block []byte) error {
	if block == nil {
		return nil
	}

	existing, err := readExistingOrEmpty(plan.configFile)
	if err != nil {
		return fmt.Errorf("read existing config for merge: %w", err)
	}
	merged, err := mergeProjectConfigBytes(existing, plan.configFile, targetPath, block)
	if err != nil {
		return err
	}
	if err := writeStagedFile(plan.tempConfigFile, merged); err != nil {
		return err
	}
	plan.configStaged = true
	return nil
}

// promotePlan registers every staged path on a SafeRenamePromoter and
// invokes Promote. On any rename failure, the promoter reverses each
// already-promoted rename before returning.
func promotePlan(plan *importPlan, renameHook func(oldpath, newpath string) error) error {
	promoter := rewrite.NewSafeRenamePromoter()
	if renameHook != nil {
		promoter.SetRenameFunc(renameHook)
	}
	promoter.StageDir(plan.tempProjectDir, plan.encodedProjectDir)
	if plan.historyStaged {
		promoter.StageFile(plan.tempHistoryFile, plan.historyFile)
	}
	if plan.configStaged {
		promoter.StageFile(plan.tempConfigFile, plan.configFile)
	}
	for _, entry := range plan.fileHistoryFiles {
		promoter.StageFile(entry.tempPath, entry.finalPath)
	}
	for _, entry := range plan.sessionKeyedStagedFiles {
		promoter.StageFile(entry.tempPath, entry.finalPath)
	}
	return promoter.Promote()
}

func ensureEmptyDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove stale staging directory %q: %w", path, err)
	}
	if err := os.MkdirAll(path, dirPerm); err != nil {
		return fmt.Errorf("create staging directory %q: %w", path, err)
	}
	return nil
}

func readExistingOrEmpty(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: trusted ClaudeHome path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

func writeStagedFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("create directories for %q: %w", path, err)
	}
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return fmt.Errorf("write staged file %q: %w", path, err)
	}
	return nil
}

func readZipFile(zipFile *zip.File) ([]byte, error) {
	readCloser, err := zipFile.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip file entry: %w", err)
	}
	defer func() { _ = readCloser.Close() }()

	data, err := io.ReadAll(readCloser)
	if err != nil {
		return nil, fmt.Errorf("read zip file entry: %w", err)
	}

	return data, nil
}

// buildHistoryBytes returns the concatenation of existing and each appended
// slice, separating them with newlines when the existing content does not
// already end with one. Centralising this here lets the staging layer write
// the result atomically instead of appending to the real history file in
// the middle of a loop.
func buildHistoryBytes(existing []byte, appends [][]byte) []byte {
	var buffer []byte
	buffer = append(buffer, existing...)
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		buffer = append(buffer, '\n')
	}
	for _, chunk := range appends {
		buffer = append(buffer, chunk...)
	}
	return buffer
}

// mergeProjectConfigBytes returns the JSON bytes of existingData with
// blockData spliced in as the project entry under targetPath. It uses sjson
// to preserve every byte outside the inserted entry — original key order,
// indent style, and trailing newlines all survive. If existingData is empty,
// a minimal `{}` is used as the base document. configPath is used only in
// error messages.
func mergeProjectConfigBytes(existingData []byte, configPath, targetPath string, blockData []byte) ([]byte, error) {
	if len(existingData) == 0 {
		existingData = []byte(`{}`)
	} else if !gjson.ValidBytes(existingData) {
		return nil, fmt.Errorf("invalid JSON in config file %q", configPath)
	}

	path := "projects." + rewrite.EscapeSJSONKey(targetPath)
	updatedData, err := sjson.SetRawBytes(existingData, path, blockData)
	if err != nil {
		return nil, fmt.Errorf("set project block in config file %q: %w", configPath, err)
	}
	return updatedData, nil
}
