package importer_test

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/testutil"
)

// addEntry is a helper type for adding entries to a test ZIP archive.
type addEntry func(zipName string, content []byte)

// buildTestArchive constructs a cc-port ZIP archive from fixture data,
// replacing source paths with placeholder tokens so the importer can resolve
// them at import time.
//
// The archive contains:
//   - metadata.xml
//   - sessions/99999.json  (from fixture sessions/ directory)
//   - memory/MEMORY.md
//   - memory/project_notes.md
//   - history/history.jsonl (only entries for sourceProjectPath)
//   - file-history/<uuid>/abcdef...@v1
//   - config.json          (the project block from .claude.json)
func buildTestArchive(
	t *testing.T,
	sourceClaudeHome *claude.Home,
	sourceProjectPath string,
	sourceHomeDir string,
	archivePath string,
) {
	t.Helper()

	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err, "create archive file")
	defer func() { _ = archiveFile.Close() }()

	zipWriter := zip.NewWriter(archiveFile)
	defer func() { _ = zipWriter.Close() }()

	// entryAdder: add a byte slice to the ZIP as zipName, replacing sourcePaths
	// with their {{PLACEHOLDER}} equivalents.
	entryAdder := addEntry(func(zipName string, content []byte) {
		t.Helper()
		content = replacePaths(content, sourceProjectPath, sourceHomeDir)
		writer, err := zipWriter.Create(zipName)
		require.NoError(t, err, "create zip entry %q", zipName)
		_, err = writer.Write(content)
		require.NoError(t, err, "write zip entry %q", zipName)
	})

	// fileAdder: add a file from the source ClaudeHome.
	fileAdder := func(zipName, sourcePath string) {
		t.Helper()
		data, err := os.ReadFile(sourcePath) //nolint:gosec // G304: test helper reading fixture files
		require.NoError(t, err, "read source file %q", sourcePath)
		entryAdder(zipName, data)
	}

	writeMetadataEntry(t, zipWriter, sourceProjectPath, sourceHomeDir)

	encodedProjectDir := sourceClaudeHome.ProjectDir(sourceProjectPath)

	// --- sessions/99999.json ---
	sessionEntry := filepath.Join(sourceClaudeHome.Dir, "sessions", "99999.json")
	if _, statErr := os.Stat(sessionEntry); statErr == nil {
		fileAdder("sessions/99999.json", sessionEntry)
	}

	// --- memory files ---
	memoryDir := filepath.Join(encodedProjectDir, "memory")
	memoryEntries, err := os.ReadDir(memoryDir)
	require.NoError(t, err, "read memory directory")
	for _, memoryEntry := range memoryEntries {
		if memoryEntry.IsDir() {
			continue
		}
		fileAdder("memory/"+memoryEntry.Name(), filepath.Join(memoryDir, memoryEntry.Name()))
	}

	// --- history/history.jsonl (filtered to this project only) ---
	historyData, err := os.ReadFile(sourceClaudeHome.HistoryFile())
	require.NoError(t, err, "read history file")
	filteredHistory := filterHistoryLines(historyData, sourceProjectPath)
	entryAdder("history/history.jsonl", filteredHistory)

	addFileHistoryEntries(t, sourceClaudeHome, fileAdder)
	addConfigEntry(t, sourceClaudeHome, sourceProjectPath, entryAdder)
}

// writeMetadataEntry writes the metadata.xml entry to the ZIP writer.
func writeMetadataEntry(
	t *testing.T,
	zipWriter *zip.Writer,
	sourceProjectPath string,
	sourceHomeDir string,
) {
	t.Helper()

	trueVal := true
	metadata := &export.Metadata{
		Export: export.Info{
			Created: time.Now(),
			Categories: []export.Category{
				{Name: "sessions", Included: true},
				{Name: "memory", Included: true},
				{Name: "history", Included: true},
				{Name: "file-history", Included: true},
				{Name: "config", Included: true},
			},
		},
		Placeholders: []export.Placeholder{
			{Key: "{{PROJECT_PATH}}", Original: sourceProjectPath, Resolvable: &trueVal},
			{Key: "{{HOME}}", Original: sourceHomeDir, Resolvable: &trueVal},
		},
	}
	metadataPath := filepath.Join(t.TempDir(), "metadata.xml")
	require.NoError(t, export.WriteManifest(metadataPath, metadata), "write temp metadata")
	metadataData, err := os.ReadFile(metadataPath) //nolint:gosec // G304: test helper reading temp file
	require.NoError(t, err, "read temp metadata")
	xmlEntry, err := zipWriter.Create("metadata.xml")
	require.NoError(t, err, "create metadata.xml entry")
	_, err = xmlEntry.Write(metadataData)
	require.NoError(t, err, "write metadata.xml")
}

// addFileHistoryEntries adds all file-history entries to the ZIP archive.
func addFileHistoryEntries(t *testing.T, sourceClaudeHome *claude.Home, fileAdder func(zipName, sourcePath string)) {
	t.Helper()

	fileHistoryBaseDir := sourceClaudeHome.FileHistoryDir()
	uuidDirs, err := os.ReadDir(fileHistoryBaseDir)
	require.NoError(t, err, "read file-history directory")
	for _, uuidDir := range uuidDirs {
		if !uuidDir.IsDir() {
			continue
		}
		versionFiles, err := os.ReadDir(filepath.Join(fileHistoryBaseDir, uuidDir.Name()))
		require.NoError(t, err, "read file-history uuid dir")
		for _, versionFile := range versionFiles {
			if versionFile.IsDir() {
				continue
			}
			zipEntryName := "file-history/" + uuidDir.Name() + "/" + versionFile.Name()
			fileAdder(zipEntryName, filepath.Join(fileHistoryBaseDir, uuidDir.Name(), versionFile.Name()))
		}
	}
}

// addConfigEntry extracts the project config block and adds it to the ZIP archive.
func addConfigEntry(
	t *testing.T,
	sourceClaudeHome *claude.Home,
	sourceProjectPath string,
	entryAdder addEntry,
) {
	t.Helper()

	configData, err := os.ReadFile(sourceClaudeHome.ConfigFile)
	require.NoError(t, err, "read config file")
	var userConfig claude.UserConfig
	require.NoError(t, json.Unmarshal(configData, &userConfig), "unmarshal config")
	projectBlock, ok := userConfig.Projects[sourceProjectPath]
	require.True(t, ok, "project %q not found in config", sourceProjectPath)
	entryAdder("config.json", []byte(projectBlock))
}

// replacePaths replaces occurrences of sourceProjectPath with {{PROJECT_PATH}}
// and sourceHomeDir with {{HOME}} in content.
func replacePaths(content []byte, sourceProjectPath, sourceHomeDir string) []byte {
	result := content
	// Replace project path first (it is a sub-path of home).
	result = []byte(strings.ReplaceAll(string(result), sourceProjectPath, "{{PROJECT_PATH}}"))
	result = []byte(strings.ReplaceAll(string(result), sourceHomeDir, "{{HOME}}"))
	return result
}

// filterHistoryLines returns only JSONL lines whose "project" field matches
// targetProject.
func filterHistoryLines(data []byte, targetProject string) []byte {
	var filtered []byte
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var entry claude.HistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Project == targetProject {
			filtered = append(filtered, []byte(line+"\n")...)
		}
	}
	return filtered
}

func TestImport_Basic(t *testing.T) {
	sourceClaudeHome := testutil.SetupFixture(t)
	sourceProjectPath := "/Users/test/Projects/myproject"
	sourceHomeDir := "/Users/test"

	archivePath := filepath.Join(t.TempDir(), "export.zip")
	buildTestArchive(t, sourceClaudeHome, sourceProjectPath, sourceHomeDir, archivePath)

	// Create a fresh destination ClaudeHome.
	destTempDir := t.TempDir()
	destClaudeDir := filepath.Join(destTempDir, "dotclaude")
	destConfigFile := filepath.Join(destTempDir, "dotclaude.json")

	require.NoError(t, os.MkdirAll(filepath.Join(destClaudeDir, "projects"), 0755)) //nolint:gosec // G301: test setup
	initialConfig := []byte(`{"projects":{}}`)
	require.NoError(t, os.WriteFile(destConfigFile, initialConfig, 0644)) //nolint:gosec // G306: test setup

	destClaudeHome := &claude.Home{
		Dir:        destClaudeDir,
		ConfigFile: destConfigFile,
	}

	destProjectPath := "/Users/dest/Projects/newproject"
	destHomeDir := filepath.Join(destTempDir, "home")

	importOptions := importer.Options{
		ArchivePath: archivePath,
		TargetPath:  destProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": destProjectPath,
			"{{HOME}}":         destHomeDir,
		},
	}

	require.NoError(t, importer.Run(destClaudeHome, importOptions))

	assertImportResults(t, destClaudeHome, destProjectPath)
}

// assertImportResults verifies that a completed import produced the expected files and content.
func assertImportResults(t *testing.T, destClaudeHome *claude.Home, destProjectPath string) {
	t.Helper()

	encodedDestProjectDir := destClaudeHome.ProjectDir(destProjectPath)
	assert.DirExists(t, encodedDestProjectDir, "encoded project dir should exist")

	// Verify memory files exist.
	assert.FileExists(t, filepath.Join(encodedDestProjectDir, "memory", "MEMORY.md"))
	assert.FileExists(t, filepath.Join(encodedDestProjectDir, "memory", "project_notes.md"))

	memoryPath := filepath.Join(encodedDestProjectDir, "memory", "MEMORY.md")
	memoryData, err := os.ReadFile(memoryPath) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	assert.NotContains(t, string(memoryData), "{{PROJECT_PATH}}", "memory file should have no unresolved placeholders")

	// Verify history was merged.
	historyData, err := os.ReadFile(destClaudeHome.HistoryFile())
	require.NoError(t, err)
	assert.NotEmpty(t, historyData, "history file should have content")
	assert.NotContains(t, string(historyData), "{{PROJECT_PATH}}", "history should have no unresolved placeholders")
	assert.Contains(t, string(historyData), destProjectPath, "history should contain resolved project path")

	assertNoPendingPlaceholders(t, encodedDestProjectDir)
	assertConfigMerged(t, destClaudeHome, destProjectPath)
}

// assertConfigMerged verifies that the project config was correctly merged into the destination config file.
func assertConfigMerged(t *testing.T, destClaudeHome *claude.Home, destProjectPath string) {
	t.Helper()

	configData, err := os.ReadFile(destClaudeHome.ConfigFile)
	require.NoError(t, err)
	var userConfig claude.UserConfig
	require.NoError(t, json.Unmarshal(configData, &userConfig))
	_, hasProject := userConfig.Projects[destProjectPath]
	assert.True(t, hasProject, "config should have the imported project entry")
}

func TestImport_ConflictRefused(t *testing.T) {
	sourceClaudeHome := testutil.SetupFixture(t)
	sourceProjectPath := "/Users/test/Projects/myproject"
	sourceHomeDir := "/Users/test"

	archivePath := filepath.Join(t.TempDir(), "export.zip")
	buildTestArchive(t, sourceClaudeHome, sourceProjectPath, sourceHomeDir, archivePath)

	// Import back to the same ClaudeHome at the same project path → conflict.
	importOptions := importer.Options{
		ArchivePath: archivePath,
		TargetPath:  sourceProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": sourceProjectPath,
			"{{HOME}}":         sourceHomeDir,
		},
	}

	err := importer.Run(sourceClaudeHome, importOptions)
	require.Error(t, err, "import to existing project should fail")
	assert.Contains(t, err.Error(), "already exists", "error should mention conflict")
}

// assertNoPendingPlaceholders walks dirPath and fails the test if any file
// contains an unresolved {{...}} placeholder token.
func assertNoPendingPlaceholders(t *testing.T, dirPath string) {
	t.Helper()

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		t.Errorf("read dir %q: %v", dirPath, err)
		return
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dirPath, entry.Name())
		if entry.IsDir() {
			assertNoPendingPlaceholders(t, fullPath)
			continue
		}
		data, err := os.ReadFile(fullPath) //nolint:gosec // G304: test-controlled path
		if err != nil {
			t.Errorf("read file %q: %v", fullPath, err)
			continue
		}
		if strings.Contains(string(data), "{{") && strings.Contains(string(data), "}}") {
			t.Errorf("file %q still contains placeholder tokens:\n%s", fullPath, string(data))
		}
	}
}
