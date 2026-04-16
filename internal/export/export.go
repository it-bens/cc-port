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
)

// CategorySet specifies which data categories to include in the export.
type CategorySet struct {
	Sessions    bool
	Memory      bool
	History     bool
	FileHistory bool
	Config      bool
}

// Options holds all parameters for an export operation.
type Options struct {
	ProjectPath  string
	OutputPath   string
	Categories   CategorySet
	Placeholders []Placeholder
}

// Run executes the export: locates project data, creates a ZIP archive at
// Options.OutputPath, and writes the requested categories with path anonymization.
func Run(claudeHome *claude.Home, exportOptions Options) error {
	locations, err := claude.LocateProject(claudeHome, exportOptions.ProjectPath)
	if err != nil {
		return fmt.Errorf("locate project: %w", err)
	}

	replacements := buildReplacementMap(exportOptions.Placeholders)

	zipFile, err := os.Create(exportOptions.OutputPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer func() { _ = zipFile.Close() }()

	archiveWriter := zip.NewWriter(zipFile)
	defer func() { _ = archiveWriter.Close() }()

	metadata := buildMetadata(exportOptions)
	metadataXMLData, err := buildMetadataXML(metadata)
	if err != nil {
		return fmt.Errorf("build metadata XML: %w", err)
	}
	if err := writeToZip(archiveWriter, "metadata.xml", metadataXMLData); err != nil {
		return fmt.Errorf("write metadata.xml: %w", err)
	}

	if err := exportSessionsIndex(archiveWriter, locations, replacements); err != nil {
		return err
	}

	if exportOptions.Categories.Sessions {
		if err := exportSessions(archiveWriter, locations, replacements); err != nil {
			return err
		}
	}

	if exportOptions.Categories.Memory {
		if err := exportMemory(archiveWriter, locations, replacements); err != nil {
			return err
		}
	}

	if exportOptions.Categories.History {
		if err := exportHistory(archiveWriter, claudeHome, exportOptions, replacements); err != nil {
			return err
		}
	}

	if exportOptions.Categories.FileHistory {
		if err := exportFileHistory(archiveWriter, locations, replacements); err != nil {
			return err
		}
	}

	if exportOptions.Categories.Config {
		if err := exportConfig(archiveWriter, claudeHome, exportOptions, replacements); err != nil {
			return err
		}
	}

	return nil
}

// exportSessionsIndex writes the sessions-index.json file to the archive if it exists.
func exportSessionsIndex(
	archiveWriter *zip.Writer, locations *claude.ProjectLocations, replacements [][2]string,
) error {
	if locations.SessionsIndex == "" {
		return nil
	}
	indexData, err := os.ReadFile(locations.SessionsIndex)
	if err != nil {
		return fmt.Errorf("read sessions-index.json: %w", err)
	}
	anonymizedIndex := anonymize(indexData, replacements)
	if err := writeToZip(archiveWriter, "sessions-index.json", anonymizedIndex); err != nil {
		return fmt.Errorf("write sessions-index.json: %w", err)
	}
	return nil
}

// exportSessions writes all session transcripts and session subdirectories to the archive.
func exportSessions(
	archiveWriter *zip.Writer, locations *claude.ProjectLocations, replacements [][2]string,
) error {
	for _, transcriptPath := range locations.SessionTranscripts {
		data, err := os.ReadFile(transcriptPath) //nolint:gosec // G304: path from trusted ClaudeHome
		if err != nil {
			return fmt.Errorf("read session transcript %s: %w", transcriptPath, err)
		}
		anonymizedData := anonymize(data, replacements)
		zipName := "sessions/" + filepath.Base(transcriptPath)
		if err := writeToZip(archiveWriter, zipName, anonymizedData); err != nil {
			return fmt.Errorf("write %s: %w", zipName, err)
		}
	}

	for _, subdirPath := range locations.SessionSubdirs {
		dirName := filepath.Base(subdirPath)
		zipPrefix := "sessions/" + dirName
		if err := addDirToZip(archiveWriter, subdirPath, zipPrefix, replacements); err != nil {
			return fmt.Errorf("add session subdir %s: %w", subdirPath, err)
		}
	}
	return nil
}

// exportMemory writes all memory files to the archive.
func exportMemory(
	archiveWriter *zip.Writer, locations *claude.ProjectLocations, replacements [][2]string,
) error {
	for _, memoryFilePath := range locations.MemoryFiles {
		data, err := os.ReadFile(memoryFilePath) //nolint:gosec // G304: path from trusted ClaudeHome
		if err != nil {
			return fmt.Errorf("read memory file %s: %w", memoryFilePath, err)
		}
		anonymizedData := anonymize(data, replacements)
		zipName := "memory/" + filepath.Base(memoryFilePath)
		if err := writeToZip(archiveWriter, zipName, anonymizedData); err != nil {
			return fmt.Errorf("write %s: %w", zipName, err)
		}
	}
	return nil
}

// exportHistory extracts and writes project history to the archive.
func exportHistory(
	archiveWriter *zip.Writer, claudeHome *claude.Home, exportOptions Options, replacements [][2]string,
) error {
	historyData, err := extractProjectHistory(claudeHome.HistoryFile(), exportOptions.ProjectPath)
	if err != nil {
		return fmt.Errorf("extract project history: %w", err)
	}
	anonymizedHistory := anonymize(historyData, replacements)
	if err := writeToZip(archiveWriter, "history/history.jsonl", anonymizedHistory); err != nil {
		return fmt.Errorf("write history/history.jsonl: %w", err)
	}
	return nil
}

// exportFileHistory writes all file-history directories to the archive.
func exportFileHistory(
	archiveWriter *zip.Writer, locations *claude.ProjectLocations, replacements [][2]string,
) error {
	for _, fileHistoryDir := range locations.FileHistoryDirs {
		dirName := filepath.Base(fileHistoryDir)
		zipPrefix := "file-history/" + dirName
		if err := addDirToZip(archiveWriter, fileHistoryDir, zipPrefix, replacements); err != nil {
			return fmt.Errorf("add file-history dir %s: %w", fileHistoryDir, err)
		}
	}
	return nil
}

// exportConfig extracts and writes the project config block to the archive.
func exportConfig(
	archiveWriter *zip.Writer, claudeHome *claude.Home, exportOptions Options, replacements [][2]string,
) error {
	configData, err := extractProjectConfig(claudeHome.ConfigFile, exportOptions.ProjectPath)
	if err != nil {
		return fmt.Errorf("extract project config: %w", err)
	}
	anonymizedConfig := anonymize(configData, replacements)
	if err := writeToZip(archiveWriter, "config.json", anonymizedConfig); err != nil {
		return fmt.Errorf("write config.json: %w", err)
	}
	return nil
}

// buildReplacementMap converts placeholders into ordered replacement pairs
// [original, key], sorted by original length descending to avoid partial
// replacements when one path is a prefix of another.
func buildReplacementMap(placeholders []Placeholder) [][2]string {
	sorted := make([]Placeholder, len(placeholders))
	copy(sorted, placeholders)
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].Original) > len(sorted[j].Original)
	})

	pairs := make([][2]string, len(sorted))
	for index, placeholder := range sorted {
		pairs[index] = [2]string{placeholder.Original, placeholder.Key}
	}
	return pairs
}

// anonymize applies all replacement pairs to data, replacing real paths with
// placeholder keys.
func anonymize(data []byte, replacements [][2]string) []byte {
	result := string(data)
	for _, pair := range replacements {
		result = strings.ReplaceAll(result, pair[0], pair[1])
	}
	return []byte(result)
}

// writeToZip adds a file with the given name and data to the archive.
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
// the archive. Text files are anonymized; binary files are copied as-is.
func addDirToZip(archiveWriter *zip.Writer, sourceDir, zipPrefix string, replacements [][2]string) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("read directory %s: %w", sourceDir, err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(sourceDir, entry.Name())
		entryZipName := zipPrefix + "/" + entry.Name()

		if entry.IsDir() {
			if err := addDirToZip(archiveWriter, entryPath, entryZipName, replacements); err != nil {
				return err
			}
			continue
		}

		data, err := os.ReadFile(entryPath) //nolint:gosec // G304: path is constructed from trusted input
		if err != nil {
			return fmt.Errorf("read file %s: %w", entryPath, err)
		}

		if isLikelyText(data) {
			data = anonymize(data, replacements)
		}

		if err := writeToZip(archiveWriter, entryZipName, data); err != nil {
			return err
		}
	}

	return nil
}

// isLikelyText is a binary-detection heuristic used to decide whether a file
// should be passed through path anonymization. We anonymize text content by
// substring replacement, which would corrupt binary payloads (file-history
// snapshots can be arbitrary bytes). A null byte in the first 512 bytes is a
// strong signal of binary content; conventional UTF-8 text never contains one.
func isLikelyText(data []byte) bool {
	checkLength := len(data)
	if checkLength > 512 {
		checkLength = 512
	}
	return !bytes.ContainsRune(data[:checkLength], 0)
}

// extractProjectHistory reads historyPath line by line and returns a JSONL byte
// slice containing only entries whose project field matches projectPath.
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

		var historyEntry claude.HistoryEntry
		if err := json.Unmarshal([]byte(line), &historyEntry); err != nil {
			continue
		}

		if historyEntry.Project != projectPath {
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
func buildMetadata(exportOptions Options) *Metadata {
	categories := []Category{
		{Name: "sessions", Included: exportOptions.Categories.Sessions},
		{Name: "memory", Included: exportOptions.Categories.Memory},
		{Name: "history", Included: exportOptions.Categories.History},
		{Name: "file-history", Included: exportOptions.Categories.FileHistory},
		{Name: "config", Included: exportOptions.Categories.Config},
	}

	return &Metadata{
		Version: 1,
		Export: Info{
			Created:    time.Now(),
			Categories: categories,
		},
		Placeholders: exportOptions.Placeholders,
	}
}

// buildMetadataXML marshals metadata to indented XML with a standard XML declaration header.
func buildMetadataXML(metadata *Metadata) ([]byte, error) {
	xmlBody, err := xml.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal metadata XML: %w", err)
	}
	return append([]byte(xml.Header), xmlBody...), nil
}
