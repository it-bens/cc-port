package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/scan"
	"github.com/it-bens/cc-port/internal/tool"
)

// Placeholders discovers placeholder suggestions for project: every path
// reference anchored under the project path or the current machine's home
// directory, found by scanning the selected categories' content, plus the
// unconditional {{PROJECT_DIR}} anchor for the project's encoded storage
// directory. Returns tool.ErrProjectAbsent when the project is unknown to
// Claude Code.
func (workspace *Workspace) Placeholders(project string, selected map[string]bool) ([]manifest.Placeholder, error) {
	locations, err := LocateProject(workspace.home, project)
	if err != nil {
		return nil, fmt.Errorf("locate project: %w", err)
	}

	content, err := gatherDiscoveryContent(locations, selected)
	if err != nil {
		return nil, err
	}

	homePath, err := homeAnchor(workspace.getenv)
	if err != nil {
		return nil, err
	}

	suggestions := discoverPlaceholders(content, project, homePath)
	placeholders := make([]manifest.Placeholder, 0, len(suggestions)+1)
	for _, suggestion := range suggestions {
		placeholders = append(placeholders, manifest.Placeholder{Key: suggestion.Key, Original: suggestion.Original})
	}
	// gatherDiscoveryContent omits the session-subdir bodies where the
	// encoded dir appears, so declare it unconditionally rather than via
	// discovery.
	placeholders = append(placeholders, manifest.Placeholder{
		Key:      "{{PROJECT_DIR}}",
		Original: workspace.home.ProjectDir(project),
	})
	return placeholders, nil
}

func gatherDiscoveryContent(locations *ProjectLocations, selected map[string]bool) ([]byte, error) {
	var content []byte
	if selected["sessions"] {
		for _, transcriptPath := range locations.SessionTranscripts {
			data, err := os.ReadFile(transcriptPath) //nolint:gosec // G304: path constructed from trusted internal data
			if err != nil {
				return nil, fmt.Errorf("read transcript %s: %w", transcriptPath, err)
			}
			content = append(content, data...)
		}
	}
	if selected["memory"] {
		for _, memoryFilePath := range locations.MemoryFiles {
			data, err := os.ReadFile(memoryFilePath) //nolint:gosec // G304: path constructed from trusted internal data
			if err != nil {
				return nil, fmt.Errorf("read memory file %s: %w", memoryFilePath, err)
			}
			content = append(content, data...)
		}
	}
	for _, sessionFilePath := range locations.SessionFiles {
		data, err := os.ReadFile(sessionFilePath) //nolint:gosec // G304: path constructed from trusted internal data
		if err != nil {
			return nil, fmt.Errorf("read session file %s: %w", sessionFilePath, err)
		}
		content = append(content, data...)
	}
	for group, path := range locations.AllFlatFiles() {
		if !selected[group.Category] {
			continue
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: path from trusted ProjectLocations
		if err != nil {
			return nil, fmt.Errorf("read %s file %s: %w", group.Name, path, err)
		}
		content = append(content, data...)
	}
	return content, nil
}

// Export implements tool.Exporter: it streams every category selected in
// selected into sink, anonymizing bodies with sink's configured
// placeholders. Returns tool.ErrProjectAbsent when the project is unknown
// to Claude Code — the generic export command writes an empty tool block
// for that case rather than failing the whole sweep.
func (workspace *Workspace) Export(
	ctx context.Context, project string, selected map[string]bool, sink *archive.Sink,
) (tool.ExportResult, error) {
	result := tool.ExportResult{Categories: make(map[string][]tool.ArchiveEntry)}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("canceled: %w", err)
	}

	locations, err := LocateProject(workspace.home, project)
	if err != nil {
		return result, fmt.Errorf("locate project: %w", err)
	}

	if selected["sessions"] {
		if err := workspace.exportSessions(ctx, sink, &result, locations); err != nil {
			return result, err
		}
	}
	if selected["memory"] {
		if err := workspace.exportMemory(ctx, sink, &result, locations); err != nil {
			return result, err
		}
	}
	if err := workspace.exportSessionKeyed(ctx, sink, &result, locations, selected); err != nil {
		return result, err
	}
	if selected["history"] {
		if err := workspace.exportHistory(ctx, sink, &result, project); err != nil {
			return result, err
		}
	}
	if selected["file-history"] {
		if err := workspace.exportFileHistory(ctx, sink, &result, locations); err != nil {
			return result, err
		}
	}
	if selected["config"] {
		if err := workspace.exportConfig(sink, &result, project); err != nil {
			return result, err
		}
	}

	report := scan.ScanReport(workspace.home.RulesDir(), project)
	if report.Err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("could not scan rules files: %v", report.Err))
	}
	for _, warning := range report.Warnings {
		result.Warnings = append(result.Warnings, fmt.Sprintf("rules file %s (line %d) references this project", warning.File, warning.Line))
	}
	if snapshotCount := len(result.Categories["file-history"]); snapshotCount > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"%d file-history snapshot(s) archived as-is; contents may still reference the original project path "+
				"(used for in-session rewinds, not persisted data)",
			snapshotCount,
		))
	}
	return result, nil
}

func recordEntry(result *tool.ExportResult, category string, written archive.WrittenEntry) {
	result.Categories[category] = append(
		result.Categories[category], tool.ArchiveEntry{ArchivePath: written.Name, Size: written.Size},
	)
}

func (workspace *Workspace) exportSessions(
	ctx context.Context, sink *archive.Sink, result *tool.ExportResult, locations *ProjectLocations,
) error {
	for _, transcriptPath := range locations.SessionTranscripts {
		if err := ctx.Err(); err != nil {
			return err
		}
		zipName := "sessions/" + filepath.Base(transcriptPath)
		written, err := writeJSONLFile(ctx, sink, zipName, transcriptPath)
		if err != nil {
			return fmt.Errorf("write %s: %w", zipName, err)
		}
		recordEntry(result, "sessions", written)
	}
	for _, subdirPath := range locations.SessionSubdirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		zipPrefix := "sessions/" + filepath.Base(subdirPath)
		if err := workspace.addDirToZip(ctx, sink, result, "sessions", subdirPath, zipPrefix); err != nil {
			return fmt.Errorf("add session subdir %s: %w", subdirPath, err)
		}
	}
	return nil
}

func writeJSONLFile(ctx context.Context, sink *archive.Sink, zipName, sourcePath string) (archive.WrittenEntry, error) {
	source, err := os.Open(sourcePath) //nolint:gosec // G304: path from trusted ClaudeHome
	if err != nil {
		return archive.WrittenEntry{}, fmt.Errorf("open %s: %w", sourcePath, err)
	}
	defer func() { _ = source.Close() }()

	sourceInfo, err := source.Stat()
	if err != nil {
		return archive.WrittenEntry{}, fmt.Errorf("stat source for %s: %w", sourcePath, err)
	}

	return sink.WriteJSONL(ctx, zipName, source, MaxHistoryLine, func(line []byte) []byte {
		// Preserve blank lines: ApplyPlaceholders on an empty body returns
		// nil, which WriteJSONL treats as "drop line".
		if len(line) == 0 {
			return line
		}
		return sink.ApplyPlaceholders(line)
	}, sourceInfo.ModTime())
}

func (workspace *Workspace) exportMemory(
	ctx context.Context, sink *archive.Sink, result *tool.ExportResult, locations *ProjectLocations,
) error {
	for _, memoryFilePath := range locations.MemoryFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		zipName := "memory/" + filepath.Base(memoryFilePath)
		written, err := writeJSONLFile(ctx, sink, zipName, memoryFilePath)
		if err != nil {
			return fmt.Errorf("write %s: %w", zipName, err)
		}
		recordEntry(result, "memory", written)
	}
	return nil
}

// exportSessionKeyed drives the zip layout for the session-keyed groups
// from registries.go, in first-seen registry order.
func (workspace *Workspace) exportSessionKeyed(
	ctx context.Context, sink *archive.Sink, result *tool.ExportResult, locations *ProjectLocations, selected map[string]bool,
) error {
	for group, path := range locations.AllFlatFiles() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !selected[group.Category] {
			continue
		}
		relative, err := filepath.Rel(group.HomeBaseDir(workspace.home), path)
		if err != nil {
			return fmt.Errorf("compute relative path for %s: %w", path, err)
		}
		zipName := group.ZipPrefix + filepath.ToSlash(relative)
		written, err := writeJSONLFile(ctx, sink, zipName, path)
		if err != nil {
			return fmt.Errorf("write %s: %w", zipName, err)
		}
		recordEntry(result, group.Category, written)
	}
	return nil
}

func (workspace *Workspace) exportHistory(
	ctx context.Context, sink *archive.Sink, result *tool.ExportResult, project string,
) error {
	historyPath := workspace.home.HistoryFile()
	historyFile, err := os.Open(historyPath) //nolint:gosec // G304: path from trusted ClaudeHome
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open history file: %w", err)
	}
	defer func() { _ = historyFile.Close() }()

	zipName := "history/history.jsonl"
	written, err := sink.WriteJSONL(ctx, zipName, historyFile, MaxHistoryLine, func(line []byte) []byte {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			return nil
		}
		if !historyLineBelongsToProject(trimmed, project) {
			return nil
		}
		return sink.ApplyPlaceholders(trimmed)
	}, time.Time{})
	if err != nil {
		return fmt.Errorf("write history/history.jsonl: %w", err)
	}
	recordEntry(result, "history", written)
	return nil
}

func (workspace *Workspace) exportFileHistory(
	ctx context.Context, sink *archive.Sink, result *tool.ExportResult, locations *ProjectLocations,
) error {
	for _, fileHistoryDir := range locations.FileHistoryDirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		zipPrefix := "file-history/" + filepath.Base(fileHistoryDir)
		if err := workspace.addDirVerbatimToZip(ctx, sink, result, fileHistoryDir, zipPrefix); err != nil {
			return fmt.Errorf("add file-history dir %s: %w", fileHistoryDir, err)
		}
	}
	return nil
}

func (workspace *Workspace) exportConfig(sink *archive.Sink, result *tool.ExportResult, project string) error {
	configData, err := extractProjectConfig(workspace.home.ConfigFile, project)
	if err != nil {
		return fmt.Errorf("extract project config: %w", err)
	}
	written, err := sink.WriteBytes("config.json", configData, time.Time{})
	if err != nil {
		return fmt.Errorf("write config.json: %w", err)
	}
	recordEntry(result, "config", written)
	return nil
}

func extractProjectConfig(configPath, projectPath string) ([]byte, error) {
	configData, err := os.ReadFile(configPath) //nolint:gosec // G304: path from trusted ClaudeHome
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("config file not found: %s", configPath)
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var userConfig UserConfig
	if err := json.Unmarshal(configData, &userConfig); err != nil {
		return nil, fmt.Errorf("unmarshal config file: %w", err)
	}

	projectBlock, exists := userConfig.Projects[projectPath]
	if !exists {
		return nil, fmt.Errorf("project %s not found in config", projectPath)
	}
	return projectBlock, nil
}

// historyLineBelongsToProject reports whether one history.jsonl line
// belongs to projectPath. See the historical internal/export package doc
// for the three-rule classification this implements.
func historyLineBelongsToProject(line []byte, projectPath string) bool {
	var historyEntry HistoryEntry
	if err := json.Unmarshal(line, &historyEntry); err != nil {
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

func (workspace *Workspace) addDirToZip(
	ctx context.Context, sink *archive.Sink, result *tool.ExportResult, category, sourceDir, zipPrefix string,
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
			if err := workspace.addDirToZip(ctx, sink, result, category, entryPath, entryZipName); err != nil {
				return err
			}
			continue
		}
		written, err := writeJSONLFile(ctx, sink, entryZipName, entryPath)
		if err != nil {
			return err
		}
		recordEntry(result, category, written)
	}
	return nil
}

func (workspace *Workspace) addDirVerbatimToZip(
	ctx context.Context, sink *archive.Sink, result *tool.ExportResult, sourceDir, zipPrefix string,
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
			if err := workspace.addDirVerbatimToZip(ctx, sink, result, entryPath, entryZipName); err != nil {
				return err
			}
			continue
		}
		source, err := os.Open(entryPath) //nolint:gosec // G304: path is constructed from trusted input
		if err != nil {
			return fmt.Errorf("open %s: %w", entryPath, err)
		}
		sourceInfo, statErr := source.Stat()
		if statErr != nil {
			_ = source.Close()
			return fmt.Errorf("stat source for %s: %w", entryPath, statErr)
		}
		written, writeErr := sink.WriteVerbatim(ctx, entryZipName, source, sourceInfo.ModTime())
		_ = source.Close()
		if writeErr != nil {
			return writeErr
		}
		recordEntry(result, "file-history", written)
	}
	return nil
}
