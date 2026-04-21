// Package export produces cc-port ZIP archives for one Claude Code project.
package export

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/transport"
)

// Options holds all parameters for an export operation.
type Options struct {
	ProjectPath  string
	OutputPath   string
	Categories   manifest.CategorySet
	Placeholders []manifest.Placeholder
}

// ArchiveEntry names one file inside the produced archive. Bodies are not
// retained; callers that need bytes reopen the zip. Keeping bodies out of
// Result means a large session export does not pin every byte in memory
// on the return path.
type ArchiveEntry struct {
	ArchivePath string
	Size        int64
}

// Result summarises the observable contents of a successful export.
// Category slices appear in manifest.AllCategories order.
type Result struct {
	// Metadata is always populated with metadata.xml.
	Metadata ArchiveEntry

	Sessions    []ArchiveEntry
	Memory      []ArchiveEntry
	History     []ArchiveEntry // zero or one entry
	FileHistory []ArchiveEntry
	Config      []ArchiveEntry // zero or one entry
	Todos       []ArchiveEntry
	UsageData   []ArchiveEntry
	PluginsData []ArchiveEntry
	Tasks       []ArchiveEntry
}

// categoryEntriesByName returns a pointer to the Result slice that receives
// archive entries for the named category. Names come from
// manifest.AllCategories; the drift-guard test in result_coverage_test.go
// ensures every AllCategories entry has a case here.
func categoryEntriesByName(result *Result, name string) (*[]ArchiveEntry, error) {
	switch name {
	case "sessions":
		return &result.Sessions, nil
	case "memory":
		return &result.Memory, nil
	case "history":
		return &result.History, nil
	case "file-history":
		return &result.FileHistory, nil
	case "config":
		return &result.Config, nil
	case "todos":
		return &result.Todos, nil
	case "usage-data":
		return &result.UsageData, nil
	case "plugins-data":
		return &result.PluginsData, nil
	case "tasks":
		return &result.Tasks, nil
	default:
		return nil, fmt.Errorf("no Result slice for category %q", name)
	}
}

// RunOption configures one call to Run.
type RunOption func(*runConfig)

type runConfig struct {
	archiveOpener func(string) (io.WriteCloser, error)
}

// defaultRunConfig returns a runConfig seeded with the production opener
// (os.Create). Callers override via WithArchiveOpener.
func defaultRunConfig() runConfig {
	return runConfig{
		archiveOpener: func(path string) (io.WriteCloser, error) {
			return os.Create(path) //nolint:gosec // G304: output path supplied by the CLI caller
		},
	}
}

// WithArchiveOpener substitutes the function Run uses to create the output
// archive file. Intended for tests that inject close-time or mid-write
// failures; production callers omit this option and receive the default
// os.Create-based opener.
func WithArchiveOpener(opener func(string) (io.WriteCloser, error)) RunOption {
	return func(config *runConfig) { config.archiveOpener = opener }
}

// Run executes the export: locates project data, creates a ZIP archive at
// Options.OutputPath, and writes the requested categories with path
// anonymization. File-history snapshots are archived verbatim — their
// contents are treated as opaque user-file bytes and are not scanned or
// rewritten. The returned Result carries one ArchiveEntry per file written,
// grouped per category; callers surface a warning when result.FileHistory
// is non-empty.
func Run(
	ctx context.Context, claudeHome *claude.Home, exportOptions Options, runOptions ...RunOption,
) (result Result, err error) {
	// Check ctx before any file creation so a cancel-before-start leaves no
	// output archive on disk. The test contract (TestRun_CancelsWhenContext
	// Cancelled) requires require.NoFileExists on the output path.
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("canceled: %w", err)
	}

	config := defaultRunConfig()
	for _, option := range runOptions {
		option(&config)
	}

	locations, err := claude.LocateProject(claudeHome, exportOptions.ProjectPath)
	if err != nil {
		return result, fmt.Errorf("locate project: %w", err)
	}

	zipFile, err := config.archiveOpener(exportOptions.OutputPath)
	if err != nil {
		return result, fmt.Errorf("create output file: %w", err)
	}
	defer func() {
		if cerr := zipFile.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close archive file: %w", cerr))
		}
	}()

	archiveWriter := zip.NewWriter(zipFile)
	defer func() {
		if cerr := archiveWriter.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("finalise archive: %w", cerr))
		}
	}()

	if err := writeMetadataToZip(archiveWriter, exportOptions, &result); err != nil {
		return result, err
	}

	placeholders := exportOptions.Placeholders

	if err := exportCoreCategories(
		ctx, archiveWriter, &result, claudeHome, locations, exportOptions, placeholders,
	); err != nil {
		return result, err
	}

	if exportOptions.Categories.FileHistory {
		if err := exportFileHistory(ctx, archiveWriter, &result, locations); err != nil {
			return result, err
		}
	}

	if exportOptions.Categories.Config {
		if err := exportConfig(archiveWriter, &result, claudeHome, exportOptions, placeholders); err != nil {
			return result, err
		}
	}

	return result, nil
}

func writeMetadataToZip(archiveWriter *zip.Writer, exportOptions Options, result *Result) error {
	metadata := buildMetadata(exportOptions)
	metadataXMLData, err := buildMetadataXML(metadata)
	if err != nil {
		return fmt.Errorf("build metadata XML: %w", err)
	}
	size, err := writeToZip(archiveWriter, "metadata.xml", metadataXMLData)
	if err != nil {
		return fmt.Errorf("write metadata.xml: %w", err)
	}
	result.Metadata = ArchiveEntry{ArchivePath: "metadata.xml", Size: size}
	return nil
}

// exportCoreCategories is extracted from Run to stay within the linter's
// line-count budget.
func exportCoreCategories(
	ctx context.Context,
	archiveWriter *zip.Writer, result *Result,
	claudeHome *claude.Home, locations *claude.ProjectLocations,
	exportOptions Options, placeholders []manifest.Placeholder,
) error {
	if exportOptions.Categories.Sessions {
		if err := exportSessions(ctx, archiveWriter, result, locations, placeholders); err != nil {
			return err
		}
	}
	if exportOptions.Categories.Memory {
		if err := exportMemory(ctx, archiveWriter, result, locations, placeholders); err != nil {
			return err
		}
	}
	if err := exportSessionKeyed(
		ctx, archiveWriter, result, claudeHome, locations, exportOptions.Categories, placeholders,
	); err != nil {
		return err
	}
	if exportOptions.Categories.History {
		if err := exportHistory(ctx, archiveWriter, result, claudeHome, exportOptions, placeholders); err != nil {
			return err
		}
	}
	return nil
}

func exportSessions(
	ctx context.Context,
	archiveWriter *zip.Writer, result *Result,
	locations *claude.ProjectLocations, placeholders []manifest.Placeholder,
) error {
	for _, transcriptPath := range locations.SessionTranscripts {
		if err := ctx.Err(); err != nil {
			return err
		}
		zipName := "sessions/" + filepath.Base(transcriptPath)
		if err := streamJSONLEntry(
			ctx, archiveWriter, result, "sessions", zipName, transcriptPath, placeholders,
		); err != nil {
			return fmt.Errorf("write %s: %w", zipName, err)
		}
	}

	for _, subdirPath := range locations.SessionSubdirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		dirName := filepath.Base(subdirPath)
		zipPrefix := "sessions/" + dirName
		if err := addDirToZip(ctx, archiveWriter, result, "sessions", subdirPath, zipPrefix, placeholders); err != nil {
			return fmt.Errorf("add session subdir %s: %w", subdirPath, err)
		}
	}
	return nil
}

// streamJSONLEntry opens sourcePath, streams its contents line by line
// through applyPlaceholders into a new ZIP entry, and records the entry on
// result under category. Source file is closed before the function returns.
func streamJSONLEntry(
	ctx context.Context,
	archiveWriter *zip.Writer, result *Result, category, zipName, sourcePath string,
	placeholders []manifest.Placeholder,
) error {
	source, err := os.Open(sourcePath) //nolint:gosec // G304: path from trusted ClaudeHome
	if err != nil {
		return fmt.Errorf("open %s: %w", sourcePath, err)
	}
	defer func() { _ = source.Close() }()

	size, err := writeJSONLToZip(ctx, archiveWriter, zipName, source, func(line []byte) []byte {
		// Preserve blank lines: applyPlaceholders on an empty body routes
		// through rewrite.ReplacePathInBytes which returns nil for empty
		// inputs, and writeJSONLToZip treats nil as "drop line".
		if len(line) == 0 {
			return line
		}
		return applyPlaceholders(line, placeholders)
	})
	if err != nil {
		return err
	}
	entries, err := categoryEntriesByName(result, category)
	if err != nil {
		return err
	}
	*entries = append(*entries, ArchiveEntry{ArchivePath: zipName, Size: size})
	return nil
}

func exportMemory(
	ctx context.Context,
	archiveWriter *zip.Writer, result *Result,
	locations *claude.ProjectLocations, placeholders []manifest.Placeholder,
) error {
	for _, memoryFilePath := range locations.MemoryFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		zipName := "memory/" + filepath.Base(memoryFilePath)
		if err := streamJSONLEntry(ctx, archiveWriter, result, "memory", zipName, memoryFilePath, placeholders); err != nil {
			return fmt.Errorf("write %s: %w", zipName, err)
		}
	}
	return nil
}

// groupToCategoryName returns the manifest category name for a
// transport.SessionKeyedTarget Group. The two usage-data subgroups
// collapse onto the single "usage-data" category; every other group
// name passes through unchanged.
func groupToCategoryName(groupName string) string {
	switch groupName {
	case "usage-data/session-meta", "usage-data/facets":
		return "usage-data"
	default:
		return groupName
	}
}

// exportSessionKeyed drives the zip layout for the five session-keyed groups
// from the transport registry, iterating locations.AllFlatFiles() once and
// skipping groups whose category flag is off.
func exportSessionKeyed(
	ctx context.Context,
	archiveWriter *zip.Writer, result *Result, claudeHome *claude.Home,
	locations *claude.ProjectLocations, categories manifest.CategorySet,
	placeholders []manifest.Placeholder,
) error {
	included := map[string]bool{
		"todos":                   categories.Todos,
		"usage-data/session-meta": categories.UsageData,
		"usage-data/facets":       categories.UsageData,
		"plugins-data":            categories.PluginsData,
		"tasks":                   categories.Tasks,
	}

	baseByGroup := make(map[string]string, len(transport.SessionKeyedTargets))
	prefixByGroup := make(map[string]string, len(transport.SessionKeyedTargets))
	for _, target := range transport.SessionKeyedTargets {
		baseByGroup[target.Group] = target.HomeBaseDir(claudeHome)
		prefixByGroup[target.Group] = target.ZipPrefix
	}

	for group, path := range locations.AllFlatFiles() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !included[group.Name] {
			continue
		}
		relative, err := filepath.Rel(baseByGroup[group.Name], path)
		if err != nil {
			return fmt.Errorf("compute relative path for %s: %w", path, err)
		}
		zipName := prefixByGroup[group.Name] + filepath.ToSlash(relative)
		category := groupToCategoryName(group.Name)
		// Session-keyed bodies are small JSON or JSONL. streamJSONLEntry
		// applies placeholders per line; since paths never straddle '\n',
		// the output matches a whole-file transform byte-for-byte.
		if err := streamJSONLEntry(ctx, archiveWriter, result, category, zipName, path, placeholders); err != nil {
			return fmt.Errorf("write %s: %w", zipName, err)
		}
	}
	return nil
}

func exportHistory(
	ctx context.Context,
	archiveWriter *zip.Writer, result *Result,
	claudeHome *claude.Home, exportOptions Options, placeholders []manifest.Placeholder,
) error {
	historyPath := claudeHome.HistoryFile()
	historyFile, err := os.Open(historyPath) //nolint:gosec // G304: path from trusted ClaudeHome
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open history file: %w", err)
	}
	defer func() { _ = historyFile.Close() }()

	projectPath := exportOptions.ProjectPath
	zipName := "history/history.jsonl"
	size, err := writeJSONLToZip(ctx, archiveWriter, zipName, historyFile, func(line []byte) []byte {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			return nil
		}
		if !historyLineBelongsToProject(trimmed, projectPath) {
			return nil
		}
		return applyPlaceholders(trimmed, placeholders)
	})
	if err != nil {
		return fmt.Errorf("write history/history.jsonl: %w", err)
	}
	result.History = append(result.History, ArchiveEntry{ArchivePath: zipName, Size: size})
	return nil
}

// exportFileHistory archives every file under ~/.claude/file-history verbatim.
// No body inspection, no anonymisation — opaque by policy.
func exportFileHistory(
	ctx context.Context,
	archiveWriter *zip.Writer, result *Result, locations *claude.ProjectLocations,
) error {
	for _, fileHistoryDir := range locations.FileHistoryDirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		dirName := filepath.Base(fileHistoryDir)
		zipPrefix := "file-history/" + dirName
		if err := addDirVerbatimToZip(ctx, archiveWriter, result, fileHistoryDir, zipPrefix); err != nil {
			return fmt.Errorf("add file-history dir %s: %w", fileHistoryDir, err)
		}
	}
	return nil
}

func exportConfig(
	archiveWriter *zip.Writer, result *Result,
	claudeHome *claude.Home, exportOptions Options, placeholders []manifest.Placeholder,
) error {
	configData, err := extractProjectConfig(claudeHome.ConfigFile, exportOptions.ProjectPath)
	if err != nil {
		return fmt.Errorf("extract project config: %w", err)
	}
	anonymizedConfig := applyPlaceholders(configData, placeholders)
	if err := writeCategoryEntry(archiveWriter, result, "config", "config.json", anonymizedConfig); err != nil {
		return fmt.Errorf("write config.json: %w", err)
	}
	return nil
}

// applyPlaceholders rewrites every placeholder's Original path to its Key
// token in data, using rewrite.ReplacePathInBytes so sibling-path prefix
// collisions (e.g. `/Users/x/myproject-extras` vs `/Users/x/myproject`) are
// never corrupted by substring replacement.
//
// Placeholders are applied in descending Original length because
// boundary-aware replacement only prevents collisions that cross a
// path-component boundary (`myproject` vs `myproject-extras`); it does NOT
// prevent a shorter placeholder from consuming a legitimate prefix of a
// longer one that ends at a real `/` boundary. For example, substituting
// `/Users/x` → `{{HOME}}` before `/Users/x/project` → `{{PROJECT_PATH}}`
// would leave `{{HOME}}/project` where `{{PROJECT_PATH}}` was intended.
// Sorting longest-first resolves this.
func applyPlaceholders(data []byte, placeholders []manifest.Placeholder) []byte {
	sorted := make([]manifest.Placeholder, len(placeholders))
	copy(sorted, placeholders)
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].Original) > len(sorted[j].Original)
	})
	for _, placeholder := range sorted {
		data, _ = rewrite.ReplacePathInBytes(data, placeholder.Original, placeholder.Key)
	}
	return data
}

// writeToZip creates and writes one archive entry. It returns the entry's
// size (the length of data in bytes) so callers can record an ArchiveEntry
// in the matching Result slice.
func writeToZip(archiveWriter *zip.Writer, name string, data []byte) (int64, error) {
	writer, err := archiveWriter.Create(name)
	if err != nil {
		return 0, fmt.Errorf("create zip entry %s: %w", name, err)
	}
	if _, err := writer.Write(data); err != nil {
		return 0, fmt.Errorf("write zip entry %s: %w", name, err)
	}
	return int64(len(data)), nil
}

// writeReaderToZip streams src into a new ZIP entry named name. Honours ctx
// so long streams abort promptly on cancellation. Returns the bytes written.
// No transform is applied; chunk-level substring substitution would corrupt
// content that straddles a read boundary, so callers needing byte
// transforms must use writeJSONLToZip.
func writeReaderToZip(
	ctx context.Context,
	archiveWriter *zip.Writer,
	name string,
	src io.Reader,
) (int64, error) {
	entry, err := archiveWriter.Create(name)
	if err != nil {
		return 0, fmt.Errorf("create zip entry %s: %w", name, err)
	}
	var written int64
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			if _, writeErr := entry.Write(buffer[:n]); writeErr != nil {
				return written, fmt.Errorf("write zip entry %s: %w", name, writeErr)
			}
			written += int64(n)
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, fmt.Errorf("read source for %s: %w", name, readErr)
		}
	}
}

// writeJSONLToZip streams src line by line through lineTransform (if
// non-nil) into a new ZIP entry named name. The original line terminator
// ('\n' or absence thereof) is preserved so the archive entry is
// byte-identical to what a whole-file transform would produce. ctx is
// checked at each line boundary. Oversized lines produce bufio.ErrTooLong
// via claude.MaxHistoryLine. Returns the bytes written.
//
// A lineTransform that returns a nil slice drops the line entirely: the
// body and its terminator are both skipped, so the output contains no
// orphan blank line in place of a dropped entry. Returning an empty
// non-nil slice (`[]byte{}`) is distinct: it writes a zero-byte body
// followed by the preserved terminator.
func writeJSONLToZip(
	ctx context.Context,
	archiveWriter *zip.Writer,
	name string,
	src io.Reader,
	lineTransform func([]byte) []byte,
) (int64, error) {
	entry, err := archiveWriter.Create(name)
	if err != nil {
		return 0, fmt.Errorf("create zip entry %s: %w", name, err)
	}
	reader := bufio.NewReaderSize(src, 64<<10)
	var written int64
	lineNumber := 0
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		line, readErr := reader.ReadBytes('\n')
		if int64(len(line)) > claude.MaxHistoryLine {
			return written, fmt.Errorf(
				"%s line %d exceeds %d bytes: %w",
				name, lineNumber+1, claude.MaxHistoryLine, bufio.ErrTooLong,
			)
		}
		if len(line) > 0 {
			lineNumber++
			body, terminator := splitJSONLTerminator(line)
			out := body
			if lineTransform != nil {
				out = lineTransform(body)
			}
			if out != nil {
				if _, err := entry.Write(out); err != nil {
					return written, fmt.Errorf("write zip entry %s: %w", name, err)
				}
				written += int64(len(out))
				if len(terminator) > 0 {
					if _, err := entry.Write(terminator); err != nil {
						return written, fmt.Errorf("write zip entry %s: %w", name, err)
					}
					written += int64(len(terminator))
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, fmt.Errorf("read source for %s: %w", name, readErr)
		}
	}
}

// splitJSONLTerminator separates a line read by bufio.Reader.ReadBytes('\n')
// into its body and the trailing '\n' terminator (empty when the last line
// has no terminator). Mirrors splitLineTerminator in internal/rewrite; kept
// local so the zip writer owns its own primitive.
func splitJSONLTerminator(line []byte) (body, terminator []byte) {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		return line[:len(line)-1], line[len(line)-1:]
	}
	return line, nil
}

// writeCategoryEntry writes one archive entry and appends a matching
// ArchiveEntry to the Result slice for category. Returns an error on
// unknown category (drift guard — cannot happen for production category
// names, and is caught at test time by TestResult_CoversEveryManifestCategory).
func writeCategoryEntry(
	archiveWriter *zip.Writer, result *Result, category, name string, data []byte,
) error {
	size, err := writeToZip(archiveWriter, name, data)
	if err != nil {
		return err
	}
	entries, err := categoryEntriesByName(result, category)
	if err != nil {
		return err
	}
	*entries = append(*entries, ArchiveEntry{ArchivePath: name, Size: size})
	return nil
}

// addDirToZip is only called by exportSessions' session-subdir walk, whose
// content is always textual (JSONL transcripts, subagent files, session-memory
// entries).
func addDirToZip(
	ctx context.Context,
	archiveWriter *zip.Writer, result *Result, category string,
	sourceDir, zipPrefix string, placeholders []manifest.Placeholder,
) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("read directory %s: %w", sourceDir, err)
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entryPath := filepath.Join(sourceDir, entry.Name())
		entryZipName := zipPrefix + "/" + entry.Name()

		if entry.IsDir() {
			if err := addDirToZip(ctx, archiveWriter, result, category, entryPath, entryZipName, placeholders); err != nil {
				return err
			}
			continue
		}

		if err := streamJSONLEntry(ctx, archiveWriter, result, category, entryZipName, entryPath, placeholders); err != nil {
			return err
		}
	}

	return nil
}

// addDirVerbatimToZip is used for file-history snapshots, whose contents are
// opaque user-file bytes that cc-port must not transform.
func addDirVerbatimToZip(
	ctx context.Context,
	archiveWriter *zip.Writer, result *Result, sourceDir, zipPrefix string,
) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("read directory %s: %w", sourceDir, err)
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entryPath := filepath.Join(sourceDir, entry.Name())
		entryZipName := zipPrefix + "/" + entry.Name()

		if entry.IsDir() {
			if err := addDirVerbatimToZip(ctx, archiveWriter, result, entryPath, entryZipName); err != nil {
				return err
			}
			continue
		}

		if err := streamVerbatimEntry(ctx, archiveWriter, result, "file-history", entryZipName, entryPath); err != nil {
			return err
		}
	}

	return nil
}

// streamVerbatimEntry streams sourcePath straight into a ZIP entry with no
// transform. Suited to opaque bytes (file-history snapshots) where any
// byte-level rewrite would violate the opacity contract.
func streamVerbatimEntry(
	ctx context.Context,
	archiveWriter *zip.Writer, result *Result, category, zipName, sourcePath string,
) error {
	source, err := os.Open(sourcePath) //nolint:gosec // G304: path is constructed from trusted input
	if err != nil {
		return fmt.Errorf("open %s: %w", sourcePath, err)
	}
	defer func() { _ = source.Close() }()

	size, err := writeReaderToZip(ctx, archiveWriter, zipName, source)
	if err != nil {
		return err
	}
	entries, err := categoryEntriesByName(result, category)
	if err != nil {
		return err
	}
	*entries = append(*entries, ArchiveEntry{ArchivePath: zipName, Size: size})
	return nil
}

// historyLineBelongsToProject reports whether one history.jsonl line
// belongs to projectPath. A line belongs when any of the following hold:
//
//  1. Its structured `project` field equals projectPath (the primary signal:
//     the authoritative tag Claude Code writes alongside each entry).
//  2. Its `project` field is empty AND the line body contains a bounded
//     reference to projectPath (e.g. inside `display` or `pastedContents`).
//     This captures entries written without a structured tag whose only link
//     to the project is a free-text path mention.
//  3. The line is not parseable as JSON AND its raw bytes contain a bounded
//     reference to projectPath. Such lines predate cc-port and cannot be
//     repaired, but an obvious textual match is still worth preserving so the
//     recipient sees what the sender saw.
//
// Lines whose `project` field names a different project are NEVER included,
// even if they happen to quote projectPath in free text: that preserves the
// privacy property that another project's structurally tagged entries are
// not leaked just because they reference this project's path.
//
// The bounded reference check goes through rewrite.ContainsBoundedPath so
// prefix-collision paths (e.g. "/a/myproject-extras" when projectPath is
// "/a/myproject") are not misclassified as in-scope.
func historyLineBelongsToProject(line []byte, projectPath string) bool {
	var historyEntry claude.HistoryEntry
	if err := json.Unmarshal(line, &historyEntry); err != nil {
		// Malformed JSON — include only when the raw bytes carry a bounded
		// reference to projectPath. Malformed lines with no such reference
		// cannot be attributed to any project and are skipped.
		return rewrite.ContainsBoundedPath(line, projectPath)
	}

	if historyEntry.Project == projectPath {
		return true
	}
	if historyEntry.Project == "" {
		return rewrite.ContainsBoundedPath(line, projectPath)
	}
	return false
}

func extractProjectConfig(configPath, projectPath string) ([]byte, error) {
	configData, err := os.ReadFile(configPath) //nolint:gosec // G304: path from trusted ClaudeHome
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("config file not found: %s", configPath)
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var userConfig claude.UserConfig
	if err := json.Unmarshal(configData, &userConfig); err != nil {
		return nil, fmt.Errorf("unmarshal config file: %w", err)
	}

	projectBlock, exists := userConfig.Projects[projectPath]
	if !exists {
		return nil, fmt.Errorf("project %s not found in config", projectPath)
	}

	return projectBlock, nil
}

func buildMetadata(exportOptions Options) *manifest.Metadata {
	return &manifest.Metadata{
		Export: manifest.Info{
			Created:    time.Now(),
			Categories: manifest.BuildCategoryEntries(&exportOptions.Categories),
		},
		Placeholders: exportOptions.Placeholders,
	}
}

func buildMetadataXML(metadata *manifest.Metadata) ([]byte, error) {
	xmlBody, err := xml.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal metadata XML: %w", err)
	}
	return append([]byte(xml.Header), xmlBody...), nil
}
