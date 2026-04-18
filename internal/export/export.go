package export

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// Result summarises observable side effects of a successful export that the
// CLI layer may want to surface to the user. Today it carries the number of
// file-history snapshots archived verbatim; callers render a warning when
// the count is positive so the user knows the archive contains un-anonymised
// user-file bytes.
type Result struct {
	FileHistorySnapshotsArchived int
}

// Run executes the export: locates project data, creates a ZIP archive at
// Options.OutputPath, and writes the requested categories with path
// anonymization. File-history snapshots are archived verbatim — their
// contents are treated as opaque user-file bytes and are not scanned or
// rewritten. The returned Result carries the number of snapshots included so
// the caller can surface a warning.
func Run(claudeHome *claude.Home, exportOptions Options) (Result, error) {
	var result Result

	locations, err := claude.LocateProject(claudeHome, exportOptions.ProjectPath)
	if err != nil {
		return result, fmt.Errorf("locate project: %w", err)
	}

	zipFile, err := os.Create(exportOptions.OutputPath)
	if err != nil {
		return result, fmt.Errorf("create output file: %w", err)
	}
	defer func() { _ = zipFile.Close() }()

	archiveWriter := zip.NewWriter(zipFile)
	defer func() { _ = archiveWriter.Close() }()

	if err := writeMetadataToZip(archiveWriter, exportOptions); err != nil {
		return result, err
	}

	placeholders := exportOptions.Placeholders

	if err := exportCoreCategories(archiveWriter, claudeHome, locations, exportOptions, placeholders); err != nil {
		return result, err
	}

	if exportOptions.Categories.FileHistory {
		archived, err := exportFileHistory(archiveWriter, locations)
		if err != nil {
			return result, err
		}
		result.FileHistorySnapshotsArchived = archived
	}

	if exportOptions.Categories.Config {
		if err := exportConfig(archiveWriter, claudeHome, exportOptions, placeholders); err != nil {
			return result, err
		}
	}

	return result, nil
}

func writeMetadataToZip(archiveWriter *zip.Writer, exportOptions Options) error {
	metadata := buildMetadata(exportOptions)
	metadataXMLData, err := buildMetadataXML(metadata)
	if err != nil {
		return fmt.Errorf("build metadata XML: %w", err)
	}
	if err := writeToZip(archiveWriter, "metadata.xml", metadataXMLData); err != nil {
		return fmt.Errorf("write metadata.xml: %w", err)
	}
	return nil
}

// exportCoreCategories runs Sessions, Memory, the four session-keyed groups,
// and History. Extracted from Run to stay within the linter's line-count
// budget.
func exportCoreCategories(
	archiveWriter *zip.Writer, claudeHome *claude.Home,
	locations *claude.ProjectLocations, exportOptions Options, placeholders []manifest.Placeholder,
) error {
	if exportOptions.Categories.Sessions {
		if err := exportSessions(archiveWriter, locations, placeholders); err != nil {
			return err
		}
	}
	if exportOptions.Categories.Memory {
		if err := exportMemory(archiveWriter, locations, placeholders); err != nil {
			return err
		}
	}
	if err := exportSessionKeyed(
		archiveWriter, claudeHome, locations, exportOptions.Categories, placeholders,
	); err != nil {
		return err
	}
	if exportOptions.Categories.History {
		if err := exportHistory(archiveWriter, claudeHome, exportOptions, placeholders); err != nil {
			return err
		}
	}
	return nil
}

func exportSessions(
	archiveWriter *zip.Writer, locations *claude.ProjectLocations, placeholders []manifest.Placeholder,
) error {
	for _, transcriptPath := range locations.SessionTranscripts {
		data, err := os.ReadFile(transcriptPath) //nolint:gosec // G304: path from trusted ClaudeHome
		if err != nil {
			return fmt.Errorf("read session transcript %s: %w", transcriptPath, err)
		}
		anonymizedData := applyPlaceholders(data, placeholders)
		zipName := "sessions/" + filepath.Base(transcriptPath)
		if err := writeToZip(archiveWriter, zipName, anonymizedData); err != nil {
			return fmt.Errorf("write %s: %w", zipName, err)
		}
	}

	for _, subdirPath := range locations.SessionSubdirs {
		dirName := filepath.Base(subdirPath)
		zipPrefix := "sessions/" + dirName
		if err := addDirToZip(archiveWriter, subdirPath, zipPrefix, placeholders); err != nil {
			return fmt.Errorf("add session subdir %s: %w", subdirPath, err)
		}
	}
	return nil
}

func exportMemory(
	archiveWriter *zip.Writer, locations *claude.ProjectLocations, placeholders []manifest.Placeholder,
) error {
	for _, memoryFilePath := range locations.MemoryFiles {
		data, err := os.ReadFile(memoryFilePath) //nolint:gosec // G304: path from trusted ClaudeHome
		if err != nil {
			return fmt.Errorf("read memory file %s: %w", memoryFilePath, err)
		}
		anonymizedData := applyPlaceholders(data, placeholders)
		zipName := "memory/" + filepath.Base(memoryFilePath)
		if err := writeToZip(archiveWriter, zipName, anonymizedData); err != nil {
			return fmt.Errorf("write %s: %w", zipName, err)
		}
	}
	return nil
}

// exportSessionKeyed drives the zip layout for the five session-keyed groups
// from the transport registry, iterating locations.AllFlatFiles() once and
// skipping groups whose category flag is off.
func exportSessionKeyed(
	archiveWriter *zip.Writer, claudeHome *claude.Home,
	locations *claude.ProjectLocations, categories manifest.CategorySet, placeholders []manifest.Placeholder,
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
		if !included[group.Name] {
			continue
		}
		relative, err := filepath.Rel(baseByGroup[group.Name], path)
		if err != nil {
			return fmt.Errorf("compute relative path for %s: %w", path, err)
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: path from trusted ProjectLocations
		if err != nil {
			return fmt.Errorf("read %s file %s: %w", group.Name, path, err)
		}
		anonymized := applyPlaceholders(data, placeholders)
		zipName := prefixByGroup[group.Name] + filepath.ToSlash(relative)
		if err := writeToZip(archiveWriter, zipName, anonymized); err != nil {
			return fmt.Errorf("write %s: %w", zipName, err)
		}
	}
	return nil
}

func exportHistory(
	archiveWriter *zip.Writer, claudeHome *claude.Home, exportOptions Options, placeholders []manifest.Placeholder,
) error {
	historyData, err := extractProjectHistory(claudeHome.HistoryFile(), exportOptions.ProjectPath)
	if err != nil {
		return fmt.Errorf("extract project history: %w", err)
	}
	anonymizedHistory := applyPlaceholders(historyData, placeholders)
	if err := writeToZip(archiveWriter, "history/history.jsonl", anonymizedHistory); err != nil {
		return fmt.Errorf("write history/history.jsonl: %w", err)
	}
	return nil
}

// exportFileHistory archives every file under ~/.claude/file-history verbatim.
// No body inspection, no anonymisation — opaque by policy.
func exportFileHistory(archiveWriter *zip.Writer, locations *claude.ProjectLocations) (int, error) {
	total := 0
	for _, fileHistoryDir := range locations.FileHistoryDirs {
		dirName := filepath.Base(fileHistoryDir)
		zipPrefix := "file-history/" + dirName
		count, err := addDirVerbatimToZip(archiveWriter, fileHistoryDir, zipPrefix)
		if err != nil {
			return total, fmt.Errorf("add file-history dir %s: %w", fileHistoryDir, err)
		}
		total += count
	}
	return total, nil
}

func exportConfig(
	archiveWriter *zip.Writer, claudeHome *claude.Home, exportOptions Options, placeholders []manifest.Placeholder,
) error {
	configData, err := extractProjectConfig(claudeHome.ConfigFile, exportOptions.ProjectPath)
	if err != nil {
		return fmt.Errorf("extract project config: %w", err)
	}
	anonymizedConfig := applyPlaceholders(configData, placeholders)
	if err := writeToZip(archiveWriter, "config.json", anonymizedConfig); err != nil {
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

func writeToZip(archiveWriter *zip.Writer, name string, data []byte) error {
	writer, err := archiveWriter.Create(name)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", name, err)
	}
	if _, err := writer.Write(data); err != nil {
		return fmt.Errorf("write zip entry %s: %w", name, err)
	}
	return nil
}

// addDirToZip recursively walks sourceDir, adding each file under zipPrefix in
// the archive and anonymising path occurrences inside each file's bytes. The
// only caller is exportSessions' session-subdir walk, whose content is always
// textual (JSONL transcripts, subagent files, session-memory entries).
func addDirToZip(archiveWriter *zip.Writer, sourceDir, zipPrefix string, placeholders []manifest.Placeholder) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("read directory %s: %w", sourceDir, err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(sourceDir, entry.Name())
		entryZipName := zipPrefix + "/" + entry.Name()

		if entry.IsDir() {
			if err := addDirToZip(archiveWriter, entryPath, entryZipName, placeholders); err != nil {
				return err
			}
			continue
		}

		data, err := os.ReadFile(entryPath) //nolint:gosec // G304: path is constructed from trusted input
		if err != nil {
			return fmt.Errorf("read file %s: %w", entryPath, err)
		}

		data = applyPlaceholders(data, placeholders)

		if err := writeToZip(archiveWriter, entryZipName, data); err != nil {
			return err
		}
	}

	return nil
}

// addDirVerbatimToZip recursively walks sourceDir, adding each file under
// zipPrefix in the archive with its bytes unchanged. Used for file-history
// snapshots, whose contents are opaque user-file bytes that cc-port must not
// transform. Returns the number of files written.
func addDirVerbatimToZip(archiveWriter *zip.Writer, sourceDir, zipPrefix string) (int, error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return 0, fmt.Errorf("read directory %s: %w", sourceDir, err)
	}

	count := 0
	for _, entry := range entries {
		entryPath := filepath.Join(sourceDir, entry.Name())
		entryZipName := zipPrefix + "/" + entry.Name()

		if entry.IsDir() {
			subCount, err := addDirVerbatimToZip(archiveWriter, entryPath, entryZipName)
			if err != nil {
				return count, err
			}
			count += subCount
			continue
		}

		data, err := os.ReadFile(entryPath) //nolint:gosec // G304: path is constructed from trusted input
		if err != nil {
			return count, fmt.Errorf("read file %s: %w", entryPath, err)
		}

		if err := writeToZip(archiveWriter, entryZipName, data); err != nil {
			return count, err
		}
		count++
	}

	return count, nil
}

// extractProjectHistory reads historyPath line by line and returns a JSONL byte
// slice containing every entry that belongs to projectPath. A line belongs
// when any of the following hold:
//
//  1. Its structured `project` field equals projectPath (the primary signal —
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
// even if they happen to quote projectPath in free text — that preserves the
// privacy property that another project's structurally tagged entries are
// not leaked just because they reference this project's path.
//
// The bounded reference check goes through rewrite.ContainsBoundedPath so
// prefix-collision paths (e.g. "/a/myproject-extras" when projectPath is
// "/a/myproject") are not misclassified as in-scope.
func extractProjectHistory(historyPath, projectPath string) ([]byte, error) {
	historyFile, err := os.Open(historyPath) //nolint:gosec // G304: path from trusted ClaudeHome
	if err != nil {
		if os.IsNotExist(err) {
			return []byte{}, nil
		}
		return nil, fmt.Errorf("open history file: %w", err)
	}
	defer func() { _ = historyFile.Close() }()

	var outputBuffer bytes.Buffer
	scanner := bufio.NewScanner(historyFile)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if !historyLineBelongsToProject([]byte(line), projectPath) {
			continue
		}

		outputBuffer.WriteString(line)
		outputBuffer.WriteByte('\n')
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan history file: %w", err)
	}

	return outputBuffer.Bytes(), nil
}

// historyLineBelongsToProject encodes the three-branch inclusion rule
// documented on extractProjectHistory.
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

// extractProjectConfig reads the config file at configPath and returns the raw
// JSON value of the projects[projectPath] block.
func extractProjectConfig(configPath, projectPath string) ([]byte, error) {
	configData, err := os.ReadFile(configPath) //nolint:gosec // G304: path from trusted ClaudeHome
	if err != nil {
		if os.IsNotExist(err) {
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

// buildMetadata constructs the Metadata value for metadata.xml from the given
// export options and the current time.
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
