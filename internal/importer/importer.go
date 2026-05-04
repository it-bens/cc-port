// Package importer handles importing cc-port ZIP archives into a Claude Code home directory.
package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/fsutil"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/scan"
	"github.com/it-bens/cc-port/internal/transport"
)

// dirPerm — 0o755, so group/others can traverse project subdirs shared with tooling.
const dirPerm = os.FileMode(0o755)

// filePerm is the mode used for files written during import.
// rw-r--r-- — owner read/write, group and others read-only, matching the
// permissions Claude Code itself writes for project data files.
const filePerm = os.FileMode(0o644)

// secretFilePerm is the mode used for files that may carry user secrets
// (history.jsonl, .claude.json, session transcripts). Locked to owner so a
// multi-user or shared-home layout does not leak pasted tokens or MCP env
// values to group or others.
const secretFilePerm = os.FileMode(0o600)

// stagingSuffix is appended to every final destination to form its temp path.
// Import writes to temp paths first, then atomically promotes them via
// SafeRenamePromoter. The suffix is distinctive enough to survive casual
// filesystem inspection if a crash ever leaves one behind.
const stagingSuffix = ".cc-port-import.tmp"

// maxZipEntryBytes caps the decompressed size of one archive entry.
// Claude session transcripts can legitimately be large; 512 MiB is two
// orders of magnitude above any real transcript and still rejects every
// known zip bomb payload.
//
// var, not const, so SetMaxEntryBytes in export_test.go can lower it for
// CI-runnable cap tests. Production paths never reassign.
var maxZipEntryBytes int64 = 512 << 20

// maxArchiveUncompressedBytes caps the aggregate decompressed size of one
// import archive. Per-entry caps alone do not prevent a crafted archive
// with N entries of maxZipEntryBytes each from exhausting memory and disk
// before any individual check fires.
//
// var, not const, for the same test-override reason as maxZipEntryBytes.
var maxArchiveUncompressedBytes int64 = 4 << 30

// stagingTempPath returns the temp path used to stage finalPath before
// atomic promotion. The temp is formed inside the symlink-resolved parent
// of finalPath so that temp and final always live on the same filesystem,
// which os.Rename requires. Without this, a symlinked parent pointing at
// another volume (e.g. ~/.claude/file-history -> /Volumes/ext/...) would
// place the sibling temp on one side of the boundary and the rename
// target on the other, and the promote step would fail with EXDEV.
func stagingTempPath(finalPath string) (string, error) {
	resolvedParent, err := fsutil.ResolveExistingAncestor(filepath.Dir(finalPath))
	if err != nil {
		return "", fmt.Errorf("resolve staging parent for %q: %w", finalPath, err)
	}
	return filepath.Join(resolvedParent, filepath.Base(finalPath)+stagingSuffix), nil
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

// Options configures an import operation. Source/Size let the importer
// accept any random-access bytes (file, decrypted tempfile, in-memory)
// without owning archive lifecycle: callers open the source, hand it to
// Run, and close it after Run returns.
type Options struct {
	Source     io.ReaderAt
	Size       int64
	TargetPath string
	// HomePath is the recipient's home directory, supplied via cmd resolveHomeAnchor()
	HomePath    string
	Resolutions map[string]string

	// renameHook lets tests inject promote-time failures. When nil, Run uses
	// os.Rename directly via SafeRenamePromoter. Package-internal by design.
	renameHook func(oldpath, newpath string) error
}

// archiveClassification captures the data runPreflight needs without
// retaining entry bodies: which declared keys appear in the archive (so the
// missing-resolution check can skip keys the archive never embeds), and
// which undeclared upper-snake tokens appear (so a tampered archive
// carrying an unlisted placeholder is rejected).
type archiveClassification struct {
	presentDeclaredKeys map[string]struct{}
	undeclaredTokens    map[string]struct{}
}

// Result summarizes the observable outcome of a successful import. The
// rules-file scan runs against TargetPath after staging promotion;
// warnings reflect post-import state.
type Result struct {
	RulesReport scan.Report
}

// Run imports a cc-port ZIP archive into claudeHome. Acquires the claudeHome
// lock, validates resolutions and staging parents up front, then reads and
// stages the archive. SafeRenamePromoter promotes all staged temps atomically
// and rolls back on any rename failure. After the promote succeeds the
// rules-scan runs against TargetPath; the returned Result carries the report.
func Run(ctx context.Context, claudeHome *claude.Home, importOptions Options) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("canceled: %w", err)
	}
	if importOptions.Source == nil {
		return nil, fmt.Errorf("importer: %w", ErrSourceNil)
	}
	var report scan.Report
	err := lock.WithLock(claudeHome, func() error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("canceled: %w", err)
		}

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

		metadata, err := manifest.ReadManifestFromZip(importOptions.Source, importOptions.Size)
		if err != nil {
			return fmt.Errorf("read metadata from archive: %w", err)
		}
		if _, err := manifest.ApplyCategoryEntries(metadata.Export.Categories); err != nil {
			return fmt.Errorf("manifest categories: %w", err)
		}

		classification, err := classifyArchive(ctx, importOptions.Source, importOptions.Size, metadata)
		if err != nil {
			return err
		}

		resolutions := withImplicitAnchors(importOptions.Resolutions, importOptions.TargetPath, importOptions.HomePath)

		if err := runPreflight(classification, metadata, resolutions); err != nil {
			return err
		}

		plan, err := buildImportPlan(
			ctx, claudeHome, importOptions.Source, importOptions.Size,
			importOptions.TargetPath, encodedProjectDir, resolutions,
		)
		if err != nil {
			// Clean up whatever temp paths the plan managed to create before
			// the error. buildImportPlan always returns a non-nil plan
			// (including on early failures), but guard explicitly so static
			// analysis is happy.
			if plan != nil {
				_ = plan.cleanupTemps()
			}
			return err
		}

		if err := promotePlan(plan, importOptions.renameHook); err != nil {
			return err
		}
		report = scan.ScanReport(claudeHome.RulesDir(), importOptions.TargetPath)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Result{RulesReport: report}, nil
}

// classifyArchive is pass one of the two-pass archive read. It walks each
// non-metadata entry on the supplied zip reader and builds an
// archiveClassification (which declared keys appear, which undeclared
// upper-snake tokens appear) without retaining any entry body. Enforces both
// the per-entry cap (maxZipEntryBytes) and the aggregate cap
// (maxArchiveUncompressedBytes).
func classifyArchive(
	ctx context.Context, src io.ReaderAt, size int64, metadata *manifest.Metadata,
) (archiveClassification, error) {
	classification := archiveClassification{
		presentDeclaredKeys: make(map[string]struct{}),
		undeclaredTokens:    make(map[string]struct{}),
	}
	declaredByKey := make(map[string]struct{}, len(metadata.Placeholders))
	for _, placeholder := range metadata.Placeholders {
		declaredByKey[placeholder.Key] = struct{}{}
	}

	zipReader, err := zip.NewReader(src, size)
	if err != nil {
		return classification, fmt.Errorf("open archive: %w", err)
	}

	var aggregate int64
	for _, zipFile := range zipReader.File {
		if err := ctx.Err(); err != nil {
			return classification, err
		}
		if zipFile.Name == "metadata.xml" {
			continue
		}
		content, err := readZipFile(zipFile)
		if err != nil {
			return classification, fmt.Errorf("read zip entry %q: %w", zipFile.Name, err)
		}
		aggregate += int64(len(content))
		if aggregate > maxArchiveUncompressedBytes {
			return classification, fmt.Errorf(
				"%w: aggregate %d > limit %d",
				ErrAggregateCapExceeded, aggregate, maxArchiveUncompressedBytes,
			)
		}
		recordPresentDeclaredKeys(content, declaredByKey, classification.presentDeclaredKeys)
		recordUndeclaredTokens(content, declaredByKey, classification.undeclaredTokens)
	}

	return classification, nil
}

// recordPresentDeclaredKeys marks each declared key that appears as a
// literal substring in body. Mirrors anyBodyContains but updates a shared
// set directly so the archive body can be discarded after each entry.
func recordPresentDeclaredKeys(body []byte, declaredByKey, presentKeys map[string]struct{}) {
	for key := range declaredByKey {
		if _, already := presentKeys[key]; already {
			continue
		}
		if bytes.Contains(body, []byte(key)) {
			presentKeys[key] = struct{}{}
		}
	}
}

// recordUndeclaredTokens scans body for `{{UPPER_SNAKE}}` tokens and marks
// each that is not in declaredByKey.
func recordUndeclaredTokens(body []byte, declaredByKey, undeclaredTokens map[string]struct{}) {
	for _, token := range rewrite.FindPlaceholderTokens(body) {
		if _, isDeclared := declaredByKey[token]; isDeclared {
			continue
		}
		undeclaredTokens[token] = struct{}{}
	}
}

// withImplicitAnchors returns a copy of resolutions that always contains
// entries for both implicit keys, injecting them from targetPath and
// homePath when the caller did not supply them. The original map is not
// mutated. Callers that already populated either key win.
func withImplicitAnchors(resolutions map[string]string, targetPath, homePath string) map[string]string {
	result := make(map[string]string, len(resolutions)+2)
	for key, value := range resolutions {
		result[key] = value
	}
	if _, has := result[projectPathKey]; !has {
		result[projectPathKey] = targetPath
	}
	if _, has := result[homePathKey]; !has {
		result[homePathKey] = homePath
	}
	return result
}

// runPreflight fails the import if any placeholder token classified in
// pass one is either declared-but-unresolved or present-but-undeclared. No
// write has occurred at this point — aborting here leaves the destination
// untouched.
func runPreflight(
	classification archiveClassification, metadata *manifest.Metadata, resolutions map[string]string,
) error {
	missing := classifyMissingResolutions(classification, metadata, resolutions)
	undeclared := sortedKeys(classification.undeclaredTokens)
	if len(missing) == 0 && len(undeclared) == 0 {
		return nil
	}

	var errs []error
	if len(missing) > 0 {
		errs = append(errs, &MissingResolutionsError{Keys: missing})
	}
	if len(undeclared) > 0 {
		errs = append(errs, &UndeclaredTokensError{Tokens: undeclared})
	}
	if len(errs) == 1 {
		return fmt.Errorf("archive preflight: %w", errs[0])
	}
	return fmt.Errorf("archive preflight: %w; %w", errs[0], errs[1])
}

// classifyMissingResolutions mirrors ClassifyPlaceholders's missing-key
// logic over the streamed classification rather than retained bodies.
// Declared keys the archive never embeds stay out of the result even when
// the caller did not provide a resolution for them.
func classifyMissingResolutions(
	classification archiveClassification, metadata *manifest.Metadata, resolutions map[string]string,
) []string {
	var missing []string
	for _, placeholder := range metadata.Placeholders {
		if placeholder.Resolvable != nil && !*placeholder.Resolvable {
			continue
		}
		if _, isResolved := resolutions[placeholder.Key]; isResolved {
			continue
		}
		if placeholder.Key == projectPathKey {
			continue
		}
		if _, present := classification.presentDeclaredKeys[placeholder.Key]; !present {
			continue
		}
		missing = append(missing, placeholder.Key)
	}
	sort.Strings(missing)
	return missing
}

// sortedKeys returns the keys of set in deterministic, alphabetical order.
func sortedKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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

// stageArchiveEntries is pass two of the two-pass archive read. It re-opens
// the archive and routes each non-metadata entry to the matching staging
// helper. Most entries stream directly from the ZIP entry reader to their
// staging temp; only the two in-memory accumulators (history appends and
// the config block) buffer a whole body. Peak memory is bounded by the
// largest of those two entries, not by the archive size.
//
// The aggregate cap is enforced a second time using actual bytes observed
// in-stream, not the zip central-directory's declared sizes, so a crafted
// archive that misdeclares sizes cannot slip through.
func stageArchiveEntries(
	ctx context.Context,
	claudeHome *claude.Home,
	plan *importPlan,
	src io.ReaderAt,
	size int64,
	resolutions map[string]string,
) (historyAppends [][]byte, configBlock []byte, err error) {
	zipReader, err := zip.NewReader(src, size)
	if err != nil {
		return nil, nil, fmt.Errorf("open archive: %w", err)
	}

	var aggregate int64
	for _, zipFile := range zipReader.File {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		if zipFile.Name == "metadata.xml" {
			continue
		}
		entryBytes, newAppends, newConfig, routeErr := routeArchiveEntry(
			claudeHome, plan, zipFile, resolutions, historyAppends, configBlock,
		)
		if routeErr != nil {
			return nil, nil, routeErr
		}
		historyAppends = newAppends
		configBlock = newConfig
		aggregate += entryBytes
		if aggregate > maxArchiveUncompressedBytes {
			return nil, nil, fmt.Errorf(
				"%w: aggregate %d > limit %d",
				ErrAggregateCapExceeded, aggregate, maxArchiveUncompressedBytes,
			)
		}
	}
	return historyAppends, configBlock, nil
}

// routeArchiveEntry dispatches one archive entry to the matching staging
// helper. Returns the number of uncompressed bytes observed so the caller
// can tally the aggregate-cap counter.
func routeArchiveEntry(
	claudeHome *claude.Home,
	plan *importPlan,
	zipFile *zip.File,
	resolutions map[string]string,
	historyAppends [][]byte,
	configBlock []byte,
) (bytesRead int64, updatedAppends [][]byte, updatedConfig []byte, err error) {
	name := zipFile.Name
	if handled, bytesRead, err := dispatchSessionKeyed(claudeHome, plan, zipFile, resolutions); err != nil {
		return bytesRead, historyAppends, configBlock, err
	} else if handled {
		return bytesRead, historyAppends, configBlock, nil
	}
	switch {
	case strings.HasPrefix(name, "sessions/"):
		bytesRead, err := stageProjectFileFromZip(plan.tempProjectDir, zipFile, "sessions/", resolutions)
		return bytesRead, historyAppends, configBlock, err
	case strings.HasPrefix(name, "memory/"):
		bytesRead, err := stageMemoryFileFromZip(plan.tempProjectDir, zipFile, resolutions)
		return bytesRead, historyAppends, configBlock, err
	case name == "history/history.jsonl":
		content, bytesRead, err := readAndResolve(zipFile, resolutions)
		if err != nil {
			return bytesRead, historyAppends, configBlock, err
		}
		return bytesRead, append(historyAppends, content), configBlock, nil
	case strings.HasPrefix(name, "file-history/"):
		staged, bytesRead, err := stageFileHistoryFromZip(claudeHome.FileHistoryDir(), zipFile)
		if err != nil {
			return bytesRead, historyAppends, configBlock, err
		}
		plan.fileHistoryFiles = append(plan.fileHistoryFiles, staged)
		return bytesRead, historyAppends, configBlock, nil
	case name == "config.json":
		content, bytesRead, err := readAndResolve(zipFile, resolutions)
		if err != nil {
			return bytesRead, historyAppends, configBlock, err
		}
		return bytesRead, historyAppends, content, nil
	default:
		return 0, historyAppends, configBlock, &UnknownArchiveEntryError{Name: name}
	}
}

// dispatchSessionKeyed streams one session-keyed entry against the first
// matching ZipPrefix in transport.SessionKeyedTargets. First prefix match
// wins. Returns (handled, bytesRead, err).
func dispatchSessionKeyed(
	claudeHome *claude.Home, plan *importPlan, zipFile *zip.File, resolutions map[string]string,
) (handled bool, bytesRead int64, err error) {
	for _, target := range transport.SessionKeyedTargets {
		if !strings.HasPrefix(zipFile.Name, target.ZipPrefix) {
			continue
		}
		staged, bytesRead, err := stageSessionKeyedFileFromZip(claudeHome, target, zipFile, resolutions)
		if err != nil {
			return true, bytesRead, err
		}
		plan.sessionKeyedStagedFiles = append(plan.sessionKeyedStagedFiles, staged)
		return true, bytesRead, nil
	}
	return false, 0, nil
}

// buildImportPlan drives pass two: it creates staging temps, streams each
// archive entry into its destination, and post-processes the accumulated
// history and config bodies. Callers invoke promotePlan on success or
// plan.cleanupTemps on failure.
func buildImportPlan(
	ctx context.Context,
	claudeHome *claude.Home,
	src io.ReaderAt,
	size int64,
	targetPath string,
	encodedProjectDir string,
	resolutions map[string]string,
) (*importPlan, error) {
	plan, err := newImportPlan(claudeHome, encodedProjectDir)
	if err != nil {
		return nil, err
	}

	if err := ensureEmptyDir(plan.tempProjectDir); err != nil {
		return plan, fmt.Errorf("stage project directory: %w", err)
	}
	plan.projectDirCreated = true

	historyAppends, configBlock, err := stageArchiveEntries(ctx, claudeHome, plan, src, size, resolutions)
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

// assertWithinRoot verifies that relativePath would be addressable via an
// os.Root opened on baseDir. It performs no write — it exists for staging
// helpers that must keep the sibling-temp layout (file-history,
// session-keyed) but still need the containment guarantee.
func assertWithinRoot(baseDir, relativePath string) error {
	if err := os.MkdirAll(baseDir, dirPerm); err != nil {
		return fmt.Errorf("%w: create %q: %w", ErrStagingFailed, baseDir, err)
	}
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return fmt.Errorf("%w: open root %q: %w", ErrStagingFailed, baseDir, err)
	}
	defer func() { _ = root.Close() }()

	relativePath = filepath.Clean(relativePath)
	if dir := filepath.Dir(relativePath); dir != "." {
		if err := root.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("%w: %q under %q: %w", ErrZipSlip, relativePath, baseDir, err)
		}
	}
	return nil
}

// stageProjectFileFromZip streams one sessions/ entry directly into the
// project staging tree, resolving placeholders on the way. Returns the
// number of bytes read from the zip entry.
func stageProjectFileFromZip(
	tempProjectDir string, zipFile *zip.File, zipPrefix string, resolutions map[string]string,
) (int64, error) {
	relativePath := strings.TrimPrefix(zipFile.Name, zipPrefix)
	return streamResolveIntoRoot(tempProjectDir, relativePath, zipFile, resolutions, secretFilePerm)
}

// stageMemoryFileFromZip streams one memory/ entry directly into the
// project staging tree under the memory/ subdirectory.
func stageMemoryFileFromZip(
	tempProjectDir string, zipFile *zip.File, resolutions map[string]string,
) (int64, error) {
	relativePath := strings.TrimPrefix(zipFile.Name, "memory/")
	return streamResolveIntoRoot(
		tempProjectDir, filepath.Join("memory", relativePath), zipFile, resolutions, filePerm,
	)
}

// stageFileHistoryFromZip streams a file-history/<uuid>/<hash>@vN entry to
// a sibling temp path of its final destination. File-history bytes are
// opaque by policy: no placeholder resolution runs over them. The os.Root
// gate on the file-history base rejects escapes before any write.
func stageFileHistoryFromZip(
	fileHistoryBaseDir string, zipFile *zip.File,
) (stagedFile, int64, error) {
	relativePath := strings.TrimPrefix(zipFile.Name, "file-history/")
	if err := assertWithinRoot(fileHistoryBaseDir, relativePath); err != nil {
		return stagedFile{}, 0, err
	}
	finalPath := filepath.Join(fileHistoryBaseDir, relativePath)
	tempPath, err := stagingTempPath(finalPath)
	if err != nil {
		return stagedFile{}, 0, err
	}
	bytesRead, err := streamVerbatimToTemp(tempPath, zipFile, filePerm)
	if err != nil {
		return stagedFile{}, bytesRead, err
	}
	return stagedFile{finalPath: finalPath, tempPath: tempPath}, bytesRead, nil
}

// stageSessionKeyedFileFromZip streams one session-keyed archive entry to
// a sibling temp path under the target group's home base directory.
// Placeholder resolution runs in-stream.
func stageSessionKeyedFileFromZip(
	claudeHome *claude.Home, target transport.SessionKeyedTarget,
	zipFile *zip.File, resolutions map[string]string,
) (stagedFile, int64, error) {
	relative := strings.TrimPrefix(zipFile.Name, target.ZipPrefix)
	baseDir := target.HomeBaseDir(claudeHome)
	if err := assertWithinRoot(baseDir, relative); err != nil {
		return stagedFile{}, 0, err
	}
	finalPath := filepath.Join(baseDir, relative)
	tempPath, err := stagingTempPath(finalPath)
	if err != nil {
		return stagedFile{}, 0, err
	}
	bytesRead, err := streamResolveToTemp(tempPath, zipFile, resolutions, filePerm)
	if err != nil {
		return stagedFile{}, bytesRead, err
	}
	return stagedFile{group: target.Group, finalPath: finalPath, tempPath: tempPath}, bytesRead, nil
}

// streamResolveIntoRoot streams a zip entry through the per-entry cap and
// ResolvePlaceholdersStream into relativePath under baseDir, using an
// os.Root handle to contain path escapes. The handle rejects `..` and
// absolute-path prefixes before any write, so a malicious zip entry name
// cannot land outside baseDir.
func streamResolveIntoRoot(
	baseDir, relativePath string, zipFile *zip.File, resolutions map[string]string, perm os.FileMode,
) (int64, error) {
	relativePath = filepath.Clean(relativePath)
	if err := os.MkdirAll(baseDir, dirPerm); err != nil {
		return 0, fmt.Errorf("%w: create %q: %w", ErrStagingFailed, baseDir, err)
	}
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return 0, fmt.Errorf("%w: open root %q: %w", ErrStagingFailed, baseDir, err)
	}
	defer func() { _ = root.Close() }()

	if dir := filepath.Dir(relativePath); dir != "." {
		if err := root.MkdirAll(dir, dirPerm); err != nil {
			return 0, fmt.Errorf("%w: %q under %q: %w", ErrZipSlip, relativePath, baseDir, err)
		}
	}

	writer, err := root.OpenFile(relativePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return 0, fmt.Errorf("%w: %q under %q: %w", ErrZipSlip, relativePath, baseDir, err)
	}
	defer func() { _ = writer.Close() }()

	bytesRead, err := streamResolveEntry(zipFile, writer, resolutions)
	if err != nil {
		return bytesRead, err
	}
	if err := writer.Close(); err != nil {
		return bytesRead, fmt.Errorf("close staged %q: %w", relativePath, err)
	}
	return bytesRead, nil
}

// streamResolveToTemp streams a zip entry through ResolvePlaceholdersStream
// into the given sibling temp path (used by file-history and session-keyed
// staging).
func streamResolveToTemp(
	tempPath string, zipFile *zip.File, resolutions map[string]string, perm os.FileMode,
) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(tempPath), dirPerm); err != nil {
		return 0, fmt.Errorf("create directories for %q: %w", tempPath, err)
	}
	//nolint:gosec // G304: tempPath constructed from resolved, containment-checked staging base
	writer, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return 0, fmt.Errorf("create staging temp %q: %w", tempPath, err)
	}
	defer func() { _ = writer.Close() }()

	bytesRead, err := streamResolveEntry(zipFile, writer, resolutions)
	if err != nil {
		return bytesRead, err
	}
	if err := writer.Close(); err != nil {
		return bytesRead, fmt.Errorf("close staging temp %q: %w", tempPath, err)
	}
	return bytesRead, nil
}

// streamVerbatimToTemp copies a zip entry byte-for-byte to the given
// sibling temp path without any placeholder resolution. Used for
// file-history snapshots, whose contents are opaque by policy.
func streamVerbatimToTemp(tempPath string, zipFile *zip.File, perm os.FileMode) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(tempPath), dirPerm); err != nil {
		return 0, fmt.Errorf("create directories for %q: %w", tempPath, err)
	}
	//nolint:gosec // G304: tempPath constructed from resolved, containment-checked staging base
	writer, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return 0, fmt.Errorf("create staging temp %q: %w", tempPath, err)
	}
	defer func() { _ = writer.Close() }()

	reader, capped, err := openCappedZipEntry(zipFile)
	if err != nil {
		return 0, err
	}
	defer func() { _ = reader.Close() }()

	bytesRead, err := io.Copy(writer, capped)
	if err != nil {
		return bytesRead, fmt.Errorf("stream zip entry %q: %w", zipFile.Name, err)
	}
	if err := enforcePostDecodeCap(zipFile.Name, bytesRead); err != nil {
		return bytesRead, err
	}
	if err := writer.Close(); err != nil {
		return bytesRead, fmt.Errorf("close staging temp %q: %w", tempPath, err)
	}
	return bytesRead, nil
}

// streamResolveEntry drives the common open + cap + resolve pipeline for
// every path that writes a resolved body. Returns the number of bytes
// observed from the zip entry.
func streamResolveEntry(
	zipFile *zip.File, writer io.Writer, resolutions map[string]string,
) (int64, error) {
	reader, capped, err := openCappedZipEntry(zipFile)
	if err != nil {
		return 0, err
	}
	defer func() { _ = reader.Close() }()

	counter := &countingReader{inner: capped}
	if err := ResolvePlaceholdersStream(counter, writer, resolutions); err != nil {
		return counter.bytesRead, fmt.Errorf("resolve zip entry %q: %w", zipFile.Name, err)
	}
	if err := enforcePostDecodeCap(zipFile.Name, counter.bytesRead); err != nil {
		return counter.bytesRead, err
	}
	return counter.bytesRead, nil
}

// openCappedZipEntry opens zipFile, rejects it up-front if its declared
// UncompressedSize64 exceeds the per-entry cap, and wraps the read side in
// an io.LimitReader sized to one byte beyond the cap. The post-decode
// counter check catches archives that misdeclare the size.
func openCappedZipEntry(zipFile *zip.File) (io.ReadCloser, io.Reader, error) {
	if zipFile.UncompressedSize64 > uint64(maxZipEntryBytes) { //nolint:gosec // G115: maxZipEntryBytes is positive by construction
		return nil, nil, fmt.Errorf(
			"%w: %q declared %d > limit %d",
			ErrEntryCapExceeded, zipFile.Name, zipFile.UncompressedSize64, maxZipEntryBytes,
		)
	}
	readCloser, err := zipFile.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("open zip entry %q: %w", zipFile.Name, err)
	}
	capped := io.LimitReader(readCloser, maxZipEntryBytes+1)
	return readCloser, capped, nil
}

// enforcePostDecodeCap rejects entries whose actual decoded size exceeds
// the per-entry cap, catching archives that misdeclare UncompressedSize64.
func enforcePostDecodeCap(name string, bytesRead int64) error {
	if bytesRead > maxZipEntryBytes {
		return fmt.Errorf(
			"%w: %q post-decode %d > limit %d",
			ErrEntryCapExceeded, name, bytesRead, maxZipEntryBytes,
		)
	}
	return nil
}

// countingReader wraps an io.Reader and remembers the total number of
// bytes successfully read. Used to attribute per-entry and aggregate
// cap bookkeeping to actual observed bytes, not the archive's declared
// sizes.
type countingReader struct {
	inner     io.Reader
	bytesRead int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	r.bytesRead += int64(n)
	return n, err
}

// readAndResolve reads one zip entry whole and applies applyResolutions.
// Used for history appends and the config block, both of which feed into
// in-memory merge logic. Enforces the per-entry cap in-stream.
func readAndResolve(zipFile *zip.File, resolutions map[string]string) (resolved []byte, bytesRead int64, err error) {
	if zipFile.UncompressedSize64 > uint64(maxZipEntryBytes) { //nolint:gosec // G115: maxZipEntryBytes is positive by construction
		return nil, 0, fmt.Errorf(
			"%w: %q declared %d > limit %d",
			ErrEntryCapExceeded, zipFile.Name, zipFile.UncompressedSize64, maxZipEntryBytes,
		)
	}
	readCloser, err := zipFile.Open()
	if err != nil {
		return nil, 0, fmt.Errorf("open zip entry %q: %w", zipFile.Name, err)
	}
	defer func() { _ = readCloser.Close() }()

	capped := io.LimitReader(readCloser, maxZipEntryBytes+1)
	data, err := io.ReadAll(capped)
	if err != nil {
		return nil, int64(len(data)), fmt.Errorf("read zip entry %q: %w", zipFile.Name, err)
	}
	if int64(len(data)) > maxZipEntryBytes {
		return nil, int64(len(data)), fmt.Errorf(
			"%w: %q post-decode %d > limit %d",
			ErrEntryCapExceeded, zipFile.Name, int64(len(data)), maxZipEntryBytes,
		)
	}
	return applyResolutions(data, resolutions), int64(len(data)), nil
}

func stageHistoryIfNeeded(plan *importPlan, appends [][]byte) error {
	if len(appends) == 0 {
		return nil
	}

	existing, err := readExistingOrEmpty(plan.historyFile)
	if err != nil {
		return fmt.Errorf("read existing history for merge: %w", err)
	}
	merged := BuildHistoryBytes(existing, appends)
	if err := writeStagedFile(plan.tempHistoryFile, merged, secretFilePerm); err != nil {
		return err
	}
	plan.historyStaged = true
	return nil
}

func stageConfigIfNeeded(plan *importPlan, targetPath string, block []byte) error {
	if block == nil {
		return nil
	}

	existing, err := readExistingOrEmpty(plan.configFile)
	if err != nil {
		return fmt.Errorf("read existing config for merge: %w", err)
	}
	merged, err := MergeProjectConfigBytes(existing, plan.configFile, targetPath, block)
	if err != nil {
		return err
	}
	if err := writeStagedFile(plan.tempConfigFile, merged, secretFilePerm); err != nil {
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
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

func writeStagedFile(path string, content []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("create directories for %q: %w", path, err)
	}
	if err := os.WriteFile(path, content, perm); err != nil {
		return fmt.Errorf("write staged file %q: %w", path, err)
	}
	return nil
}

func readZipFile(zipFile *zip.File) ([]byte, error) {
	// Pre-declared size check. Malicious archives can misdeclare
	// UncompressedSize64, so the post-decode check below is still required.
	if zipFile.UncompressedSize64 > uint64(maxZipEntryBytes) { //nolint:gosec // G115: maxZipEntryBytes is positive by construction
		return nil, fmt.Errorf(
			"%w: %q declared %d > limit %d",
			ErrEntryCapExceeded, zipFile.Name, zipFile.UncompressedSize64, maxZipEntryBytes,
		)
	}

	readCloser, err := zipFile.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip file entry: %w", err)
	}
	defer func() { _ = readCloser.Close() }()

	limited := io.LimitReader(readCloser, maxZipEntryBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read zip file entry: %w", err)
	}
	if int64(len(data)) > maxZipEntryBytes {
		return nil, fmt.Errorf(
			"%w: %q post-decode %d > limit %d",
			ErrEntryCapExceeded, zipFile.Name, int64(len(data)), maxZipEntryBytes,
		)
	}

	return data, nil
}

// BuildHistoryBytes returns existing concatenated with each appended slice,
// inserting a newline between existing and appends when existing does not
// already end with one. Pure function — no I/O, no lock. Lets the staging
// layer compute the merged bytes up-front and promote them atomically.
func BuildHistoryBytes(existing []byte, appends [][]byte) []byte {
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

// MergeProjectConfigBytes returns the JSON bytes of existingData with
// blockData spliced in as the project entry under targetPath. It uses sjson
// to preserve every byte outside the inserted entry — original key order,
// indent style, and trailing newlines all survive. If existingData is empty,
// a minimal `{}` is used as the base document. configPath is used only in
// error messages.
func MergeProjectConfigBytes(existingData []byte, configPath, targetPath string, blockData []byte) ([]byte, error) {
	if len(existingData) == 0 {
		existingData = []byte(`{}`)
	} else if !gjson.ValidBytes(existingData) {
		return nil, &InvalidConfigJSONError{Path: configPath}
	}

	path := "projects." + rewrite.EscapeSJSONKey(targetPath)
	updatedData, err := sjson.SetRawBytes(existingData, path, blockData)
	if err != nil {
		return nil, fmt.Errorf("set project block in config file %q: %w", configPath, err)
	}
	return updatedData, nil
}
