package importer_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
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
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/testutil"
)

// openArchive opens archivePath for the duration of the test and returns
// it as the (Source, Size) pair the importer's Options expects.
func openArchive(t *testing.T, archivePath string) (source io.ReaderAt, size int64) {
	t.Helper()
	zipFile, err := os.Open(archivePath) //nolint:gosec // G304: test-controlled archive path
	require.NoError(t, err, "open archive")
	t.Cleanup(func() { _ = zipFile.Close() })
	zipInfo, err := zipFile.Stat()
	require.NoError(t, err, "stat archive")
	return zipFile, zipInfo.Size()
}

// Fixture-wide constants — hardcoded in the test-data directory layout.
const (
	fixtureSourceProjectPath = "/Users/test/Projects/myproject"
	fixtureSourceHomeDir     = "/Users/test"
	fixtureDestProjectPath   = "/Users/dest/Projects/newproject"
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
	archivePath string,
) {
	t.Helper()
	sourceProjectPath := fixtureSourceProjectPath
	sourceHomeDir := fixtureSourceHomeDir

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
	metadata := &manifest.Metadata{
		Export: manifest.Info{
			Created: time.Now(),
			Categories: []manifest.Category{
				{Name: "sessions", Included: true},
				{Name: "memory", Included: true},
				{Name: "history", Included: true},
				{Name: "file-history", Included: true},
				{Name: "config", Included: true},
				{Name: "todos", Included: false},
				{Name: "usage-data", Included: false},
				{Name: "plugins-data", Included: false},
				{Name: "tasks", Included: false},
			},
		},
		Placeholders: []manifest.Placeholder{
			{Key: "{{PROJECT_PATH}}", Original: sourceProjectPath, Resolvable: &trueVal},
			{Key: "{{HOME}}", Original: sourceHomeDir, Resolvable: &trueVal},
		},
	}
	metadataPath := filepath.Join(t.TempDir(), "metadata.xml")
	require.NoError(t, manifest.WriteManifest(metadataPath, metadata), "write temp metadata")
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
	entryAdder("config.json", projectBlock)
}

// replacePaths mirrors the production export anonymizer: it rewrites
// sourceProjectPath to {{PROJECT_PATH}} and sourceHomeDir to {{HOME}} using
// path-boundary-aware substitution so prefix collisions like
// /…/myproject-extras are not corrupted into {{PROJECT_PATH}}-extras.
func replacePaths(content []byte, sourceProjectPath, sourceHomeDir string) []byte {
	content, _ = rewrite.ReplacePathInBytes(content, sourceProjectPath, "{{PROJECT_PATH}}")
	content, _ = rewrite.ReplacePathInBytes(content, sourceHomeDir, "{{HOME}}")
	return content
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

func TestImport_RestoresMemoryFiles(t *testing.T) {
	destClaudeHome := runBasicImport(t)
	projectDir := destClaudeHome.ProjectDir(fixtureDestProjectPath)

	assert.FileExists(t, filepath.Join(projectDir, "memory", "MEMORY.md"))
	assert.FileExists(t, filepath.Join(projectDir, "memory", "project_notes.md"))

	memoryPath := filepath.Join(projectDir, "memory", "MEMORY.md")
	memoryData, err := os.ReadFile(memoryPath) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	assert.NotContains(t, string(memoryData), "{{PROJECT_PATH}}",
		"memory file must have no unresolved placeholders")
}

func TestImport_MergesHistory(t *testing.T) {
	destClaudeHome := runBasicImport(t)

	historyData, err := os.ReadFile(destClaudeHome.HistoryFile())
	require.NoError(t, err)
	assert.NotEmpty(t, historyData, "history file must have content")
	assert.Contains(t, string(historyData), fixtureDestProjectPath,
		"history must reference the destination project path after resolution")
}

func TestImport_RekeysConfigBlock(t *testing.T) {
	destClaudeHome := runBasicImport(t)

	configData, err := os.ReadFile(destClaudeHome.ConfigFile)
	require.NoError(t, err)
	var userConfig claude.UserConfig
	require.NoError(t, json.Unmarshal(configData, &userConfig))

	_, hasProject := userConfig.Projects[fixtureDestProjectPath]
	assert.True(t, hasProject, "config must hold the imported project entry")
}

func TestImport_ResolvesDeclaredPlaceholders(t *testing.T) {
	destClaudeHome := runBasicImport(t)
	projectDir := destClaudeHome.ProjectDir(fixtureDestProjectPath)

	assertNoPendingPlaceholders(t, projectDir)
}

func TestImport_LandsHistoryAndConfigAt0600(t *testing.T) {
	destClaudeHome := runBasicImport(t)

	historyInfo, err := os.Stat(destClaudeHome.HistoryFile())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), historyInfo.Mode().Perm(),
		"imported history.jsonl must be owner-only")

	configInfo, err := os.Stat(destClaudeHome.ConfigFile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), configInfo.Mode().Perm(),
		"imported config file must be owner-only")
}

func TestImport_LandsSessionTranscriptAt0600(t *testing.T) {
	destClaudeHome := runBasicImport(t)
	projectDir := destClaudeHome.ProjectDir(fixtureDestProjectPath)

	entries, err := os.ReadDir(projectDir)
	require.NoError(t, err)
	var transcripts []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		transcripts = append(transcripts, entry)
	}
	require.NotEmpty(t, transcripts, "fixture must produce at least one session transcript")

	for _, transcript := range transcripts {
		info, statErr := os.Stat(filepath.Join(projectDir, transcript.Name()))
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
			"session transcript %s must be owner-only", transcript.Name())
	}
}

func TestImport_LandsMemoryFileAt0644(t *testing.T) {
	destClaudeHome := runBasicImport(t)
	projectDir := destClaudeHome.ProjectDir(fixtureDestProjectPath)

	memoryPath := filepath.Join(projectDir, "memory", "MEMORY.md")
	info, err := os.Stat(memoryPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(),
		"memory files are not sensitive and stay at the default mode")
}

// runBasicImport runs a fresh import from a cc-port archive built off the
// source fixture into an empty destination home, and returns the destination
// home for assertions.
func runBasicImport(t *testing.T) *claude.Home {
	t.Helper()

	sourceClaudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "export.zip")
	buildTestArchive(t, sourceClaudeHome, archivePath)

	destClaudeHome := buildEmptyDestClaudeHome(t)
	destHomeDir := filepath.Join(t.TempDir(), "home")

	source, size := openArchive(t, archivePath)
	_, err := importer.Run(t.Context(), destClaudeHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: fixtureDestProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": fixtureDestProjectPath,
			"{{HOME}}":         destHomeDir,
		},
	})
	require.NoError(t, err)
	return destClaudeHome
}

func TestImport_LeavesNoStagingTemps(t *testing.T) {
	sourceClaudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "export.zip")
	buildTestArchive(t, sourceClaudeHome, archivePath)

	destClaudeHome := buildEmptyDestClaudeHome(t)
	destProjectPath := fixtureDestProjectPath
	destHomeDir := filepath.Join(t.TempDir(), "home")

	source, size := openArchive(t, archivePath)
	importOptions := importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: destProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": destProjectPath,
			"{{HOME}}":         destHomeDir,
		},
	}
	_, err := importer.Run(t.Context(), destClaudeHome, importOptions)
	require.NoError(t, err)

	assertNoStagingTemps(t, destClaudeHome)
}

func TestImport_RefusesUnresolvedDeclaredKey(t *testing.T) {
	sourceClaudeHome := testutil.SetupFixture(t)

	archivePath := filepath.Join(t.TempDir(), "export.zip")
	buildArchiveWithExtraDeclaredKey(
		t, sourceClaudeHome, archivePath, "{{EXTRA}}", true,
	)

	destClaudeHome := buildEmptyDestClaudeHome(t)
	destProjectPath := fixtureDestProjectPath

	preConfigBytes, err := os.ReadFile(destClaudeHome.ConfigFile)
	require.NoError(t, err)

	source, size := openArchive(t, archivePath)
	importOptions := importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: destProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": destProjectPath,
			"{{HOME}}":         filepath.Join(t.TempDir(), "home"),
		},
	}
	_, err = importer.Run(t.Context(), destClaudeHome, importOptions)
	require.Error(t, err, "import must refuse when a declared placeholder is not resolved")
	assert.Contains(t, err.Error(), "{{EXTRA}}")

	assertImportLeftDestinationUntouched(t, destClaudeHome, destProjectPath, preConfigBytes)
}

func TestImport_AllowsUnresolvableDeclaredKey(t *testing.T) {
	sourceClaudeHome := testutil.SetupFixture(t)

	archivePath := filepath.Join(t.TempDir(), "export.zip")
	// Declare {{LEGACY}} with Resolvable=false and inject a literal
	// occurrence into the memory body. The caller supplies no resolution;
	// the preflight gate must allow the import to succeed and the literal
	// must survive on disk.
	buildArchiveWithExtraDeclaredKey(
		t, sourceClaudeHome, archivePath, "{{LEGACY}}", false,
	)

	destClaudeHome := buildEmptyDestClaudeHome(t)
	destProjectPath := fixtureDestProjectPath

	source, size := openArchive(t, archivePath)
	importOptions := importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: destProjectPath,
		Resolutions: map[string]string{
			"{{HOME}}": filepath.Join(t.TempDir(), "home"),
		},
	}
	_, err := importer.Run(t.Context(), destClaudeHome, importOptions)
	require.NoError(t, err)

	memoryPath := filepath.Join(
		destClaudeHome.ProjectDir(destProjectPath), "memory", "MEMORY.md",
	)
	data, err := os.ReadFile(memoryPath) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	assert.Contains(t, string(data), "{{LEGACY}}",
		"Resolvable=false placeholder must survive import verbatim")
}

func TestImport_RefusesUndeclaredKey(t *testing.T) {
	sourceClaudeHome := testutil.SetupFixture(t)

	archivePath := filepath.Join(t.TempDir(), "export.zip")
	// Inject {{SECRET}} into a body WITHOUT declaring it in the manifest.
	buildArchiveWithUndeclaredBodyToken(
		t, sourceClaudeHome, archivePath, "{{SECRET}}",
	)

	destClaudeHome := buildEmptyDestClaudeHome(t)
	destProjectPath := fixtureDestProjectPath

	preConfigBytes, err := os.ReadFile(destClaudeHome.ConfigFile)
	require.NoError(t, err)

	source, size := openArchive(t, archivePath)
	importOptions := importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: destProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": destProjectPath,
			"{{HOME}}":         filepath.Join(t.TempDir(), "home"),
		},
	}
	_, err = importer.Run(t.Context(), destClaudeHome, importOptions)
	require.Error(t, err, "import must refuse an archive carrying an undeclared token")
	assert.Contains(t, err.Error(), "{{SECRET}}")

	assertImportLeftDestinationUntouched(t, destClaudeHome, destProjectPath, preConfigBytes)
}

func TestImport_AtomicRollbackOnFailure(t *testing.T) {
	sourceClaudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "export.zip")
	buildTestArchive(t, sourceClaudeHome, archivePath)

	destClaudeHome := buildEmptyDestClaudeHome(t)
	destProjectPath := fixtureDestProjectPath

	// Snapshot pre-import bytes so we can assert nothing was mutated after rollback.
	preConfigBytes, err := os.ReadFile(destClaudeHome.ConfigFile)
	require.NoError(t, err)
	require.NoFileExists(t, destClaudeHome.HistoryFile())

	// Fail the second rename — the first (project dir) has already promoted,
	// so rollback must un-promote it.
	callCount := 0
	injector := func(oldpath, newpath string) error {
		callCount++
		if callCount == 2 {
			return errors.New("simulated promote failure")
		}
		return os.Rename(oldpath, newpath)
	}

	source, size := openArchive(t, archivePath)
	importOptions := importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: destProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": destProjectPath,
			"{{HOME}}":         filepath.Join(t.TempDir(), "home"),
		},
	}
	err = importer.RunWithRenameHook(t.Context(), destClaudeHome, importOptions, injector)
	require.Error(t, err, "import must fail when a promote rename fails")

	// Encoded project dir must not exist — it was promoted then rolled back.
	assert.NoDirExists(t, destClaudeHome.ProjectDir(destProjectPath),
		"rollback must remove the promoted project directory")

	// Config file must match pre-import bytes.
	postConfigBytes, err := os.ReadFile(destClaudeHome.ConfigFile)
	require.NoError(t, err)
	assert.Equal(t, preConfigBytes, postConfigBytes,
		"rollback must restore config file bytes")

	assert.NoFileExists(t, destClaudeHome.HistoryFile(),
		"rollback must leave history absent when it was absent pre-import")

	// No staging temps must remain.
	assertNoStagingTemps(t, destClaudeHome)
}

// buildEmptyDestClaudeHome creates a fresh empty ClaudeHome with a minimal
// config file. Shared by the import tests that need an untouched target.
func buildEmptyDestClaudeHome(t *testing.T) *claude.Home {
	t.Helper()

	destTempDir := t.TempDir()
	destClaudeDir := filepath.Join(destTempDir, "dotclaude")
	destConfigFile := filepath.Join(destTempDir, "dotclaude.json")

	require.NoError(t, os.MkdirAll(filepath.Join(destClaudeDir, "projects"), 0o755)) //nolint:gosec // G301: test setup
	initialConfig := []byte(`{"projects":{}}`)
	require.NoError(t, os.WriteFile(destConfigFile, initialConfig, 0o644)) //nolint:gosec // G306: test setup

	return &claude.Home{
		Dir:        destClaudeDir,
		ConfigFile: destConfigFile,
	}
}

// assertImportLeftDestinationUntouched verifies that a refused import did
// not mutate the destination.
func assertImportLeftDestinationUntouched(
	t *testing.T, destClaudeHome *claude.Home, destProjectPath string, preConfigBytes []byte,
) {
	t.Helper()

	assert.NoDirExists(t, destClaudeHome.ProjectDir(destProjectPath),
		"refused import must not create the encoded project directory")
	assert.NoFileExists(t, destClaudeHome.HistoryFile(),
		"refused import must not create the history file")

	postConfigBytes, err := os.ReadFile(destClaudeHome.ConfigFile)
	require.NoError(t, err)
	assert.Equal(t, preConfigBytes, postConfigBytes,
		"refused import must not modify the config file")

	assertNoStagingTemps(t, destClaudeHome)
}

// assertNoStagingTemps walks the home dir and asserts no *.cc-port-import.tmp
// paths remain.
func assertNoStagingTemps(t *testing.T, destClaudeHome *claude.Home) {
	t.Helper()

	walkRoots := []string{destClaudeHome.Dir, filepath.Dir(destClaudeHome.ConfigFile)}
	for _, root := range walkRoots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if strings.HasSuffix(path, ".cc-port-import.tmp") {
				t.Errorf("staging temp %q must not remain after run", path)
			}
			return nil
		})
	}
}

// buildArchiveWithExtraDeclaredKey builds a test archive identical to
// buildTestArchive but additionally declares extraKey in the manifest with
// the given Resolvable value and injects one literal occurrence of extraKey
// into the MEMORY.md body so it is guaranteed to show up in preflight.
func buildArchiveWithExtraDeclaredKey(
	t *testing.T,
	sourceClaudeHome *claude.Home,
	archivePath string,
	extraKey string,
	resolvable bool,
) {
	t.Helper()
	buildArchiveWithOverrides(t, archiveOverrides{
		sourceClaudeHome: sourceClaudeHome,
		archivePath:      archivePath,
		extraDeclaredKey: extraKey,
		extraResolvable:  &resolvable,
		memoryInjection:  extraKey,
	})
}

// buildArchiveWithUndeclaredBodyToken injects tokenInBody into the memory
// body without declaring it in the manifest.
func buildArchiveWithUndeclaredBodyToken(
	t *testing.T,
	sourceClaudeHome *claude.Home,
	archivePath string,
	tokenInBody string,
) {
	t.Helper()
	buildArchiveWithOverrides(t, archiveOverrides{
		sourceClaudeHome: sourceClaudeHome,
		archivePath:      archivePath,
		memoryInjection:  tokenInBody,
	})
}

// archiveOverrides parameterises buildArchiveWithOverrides. Fields are
// optional; the zero values produce an archive identical to the one
// buildTestArchive builds. sourceProjectPath and sourceHomeDir are always
// the fixture constants.
type archiveOverrides struct {
	sourceClaudeHome *claude.Home
	archivePath      string
	extraDeclaredKey string
	extraResolvable  *bool
	memoryInjection  string
}

// buildArchiveWithOverrides is a lower-level builder that allows test
// archives to deviate from the default shape: declaring additional keys in
// the manifest and/or injecting literal tokens into the memory body.
func buildArchiveWithOverrides(t *testing.T, overrides archiveOverrides) {
	t.Helper()

	archiveFile, err := os.Create(overrides.archivePath)
	require.NoError(t, err)
	defer func() { _ = archiveFile.Close() }()

	zipWriter := zip.NewWriter(archiveFile)
	defer func() { _ = zipWriter.Close() }()

	writeMetadataEntryWithOverrides(t, zipWriter, overrides)

	encodedProjectDir := overrides.sourceClaudeHome.ProjectDir(fixtureSourceProjectPath)

	entryAdder := addEntry(func(zipName string, content []byte) {
		t.Helper()
		if overrides.memoryInjection != "" && zipName == "memory/MEMORY.md" {
			content = append(content, []byte("\n"+overrides.memoryInjection+"\n")...)
		}
		content = replacePaths(content, fixtureSourceProjectPath, fixtureSourceHomeDir)
		writer, err := zipWriter.Create(zipName)
		require.NoError(t, err)
		_, err = writer.Write(content)
		require.NoError(t, err)
	})

	fileAdder := func(zipName, sourcePath string) {
		t.Helper()
		data, err := os.ReadFile(sourcePath) //nolint:gosec // G304: test helper reading fixture files
		require.NoError(t, err)
		entryAdder(zipName, data)
	}

	sessionEntry := filepath.Join(overrides.sourceClaudeHome.Dir, "sessions", "99999.json")
	if _, statErr := os.Stat(sessionEntry); statErr == nil {
		fileAdder("sessions/99999.json", sessionEntry)
	}

	memoryEntries, err := os.ReadDir(filepath.Join(encodedProjectDir, "memory"))
	require.NoError(t, err)
	for _, memoryEntry := range memoryEntries {
		if memoryEntry.IsDir() {
			continue
		}
		fileAdder("memory/"+memoryEntry.Name(),
			filepath.Join(encodedProjectDir, "memory", memoryEntry.Name()))
	}

	historyData, err := os.ReadFile(overrides.sourceClaudeHome.HistoryFile())
	require.NoError(t, err)
	entryAdder("history/history.jsonl",
		filterHistoryLines(historyData, fixtureSourceProjectPath))

	addFileHistoryEntries(t, overrides.sourceClaudeHome, fileAdder)
	addConfigEntry(t, overrides.sourceClaudeHome, fixtureSourceProjectPath, entryAdder)
}

// writeMetadataEntryWithOverrides is the parameterised cousin of
// writeMetadataEntry that honors archiveOverrides.extraDeclaredKey and
// extraResolvable.
func writeMetadataEntryWithOverrides(t *testing.T, zipWriter *zip.Writer, overrides archiveOverrides) {
	t.Helper()

	trueVal := true
	placeholders := []manifest.Placeholder{
		{Key: "{{PROJECT_PATH}}", Original: fixtureSourceProjectPath, Resolvable: &trueVal},
		{Key: "{{HOME}}", Original: fixtureSourceHomeDir, Resolvable: &trueVal},
	}
	if overrides.extraDeclaredKey != "" {
		placeholders = append(placeholders, manifest.Placeholder{
			Key:        overrides.extraDeclaredKey,
			Original:   overrides.extraDeclaredKey,
			Resolvable: overrides.extraResolvable,
		})
	}

	metadata := &manifest.Metadata{
		Export: manifest.Info{
			Created: time.Now(),
			Categories: []manifest.Category{
				{Name: "sessions", Included: true},
				{Name: "memory", Included: true},
				{Name: "history", Included: true},
				{Name: "file-history", Included: true},
				{Name: "config", Included: true},
				{Name: "todos", Included: false},
				{Name: "usage-data", Included: false},
				{Name: "plugins-data", Included: false},
				{Name: "tasks", Included: false},
			},
		},
		Placeholders: placeholders,
	}
	metadataPath := filepath.Join(t.TempDir(), "metadata.xml")
	require.NoError(t, manifest.WriteManifest(metadataPath, metadata))
	metadataData, err := os.ReadFile(metadataPath) //nolint:gosec // G304: test helper reading temp file
	require.NoError(t, err)
	xmlEntry, err := zipWriter.Create("metadata.xml")
	require.NoError(t, err)
	_, err = xmlEntry.Write(metadataData)
	require.NoError(t, err)
}

func TestImport_ConflictRefused(t *testing.T) {
	sourceClaudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "export.zip")
	buildTestArchive(t, sourceClaudeHome, archivePath)

	// Import back to the same ClaudeHome at the same project path → conflict.
	source, size := openArchive(t, archivePath)
	importOptions := importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: fixtureSourceProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": fixtureSourceProjectPath,
			"{{HOME}}":         fixtureSourceHomeDir,
		},
	}

	_, err := importer.Run(t.Context(), sourceClaudeHome, importOptions)
	require.Error(t, err, "import to existing project should fail")
	require.ErrorIs(t, err, importer.ErrEncodedDirCollision)
}

func TestImport_RoundTrip_NewCategories(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "out.zip")

	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled tempdir path
	require.NoError(t, err)
	_, err = export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: fixtureSourceProjectPath,
		Output:      archiveFile,
		Categories: manifest.CategorySet{
			Sessions: true, Memory: true, History: true, Config: true,
			Todos: true, UsageData: true, PluginsData: true, Tasks: true,
		},
	})
	require.NoError(t, err)
	require.NoError(t, archiveFile.Close())

	freshHome := testutil.SetupFixture(t)
	require.NoError(t, os.RemoveAll(freshHome.TodosDir()))
	require.NoError(t, os.RemoveAll(freshHome.UsageDataDir()))
	require.NoError(t, os.RemoveAll(freshHome.PluginsDataDir()))
	require.NoError(t, os.RemoveAll(freshHome.TasksDir()))
	// freshHome already has the project dir, so remove it to avoid CheckConflict.
	require.NoError(t, os.RemoveAll(freshHome.ProjectDir(fixtureSourceProjectPath)))

	source, size := openArchive(t, archivePath)
	_, err = importer.Run(t.Context(), freshHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: fixtureSourceProjectPath,
	})
	require.NoError(t, err)

	imported, err := claude.LocateProject(freshHome, fixtureSourceProjectPath)
	require.NoError(t, err)
	assert.NotEmpty(t, imported.TodoFiles)
	assert.NotEmpty(t, imported.UsageDataSessionMeta)
	assert.NotEmpty(t, imported.UsageDataFacets)
	assert.NotEmpty(t, imported.PluginsDataFiles)
	assert.NotEmpty(t, imported.TaskFiles)
}

func TestImport_LandsSessionKeyedFileAt0644(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "out.zip")

	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled tempdir path
	require.NoError(t, err)
	_, err = export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: fixtureSourceProjectPath,
		Output:      archiveFile,
		Categories: manifest.CategorySet{
			Sessions: true, Memory: true, History: true, Config: true,
			Todos: true, UsageData: true, PluginsData: true, Tasks: true,
		},
	})
	require.NoError(t, err)
	require.NoError(t, archiveFile.Close())

	freshHome := testutil.SetupFixture(t)
	require.NoError(t, os.RemoveAll(freshHome.TodosDir()))
	require.NoError(t, os.RemoveAll(freshHome.UsageDataDir()))
	require.NoError(t, os.RemoveAll(freshHome.PluginsDataDir()))
	require.NoError(t, os.RemoveAll(freshHome.TasksDir()))
	require.NoError(t, os.RemoveAll(freshHome.ProjectDir(fixtureSourceProjectPath)))

	source, size := openArchive(t, archivePath)
	_, err = importer.Run(t.Context(), freshHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: fixtureSourceProjectPath,
	})
	require.NoError(t, err)

	imported, err := claude.LocateProject(freshHome, fixtureSourceProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, imported.TodoFiles,
		"round-trip must stage at least one todo file")

	info, err := os.Stat(imported.TodoFiles[0])
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(),
		"session-keyed todo entry is metadata, not a secret, and stays at the default mode")
}

func TestImport_HardFailsOnUnknownManifestCategory(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	require.NoError(t, os.RemoveAll(claudeHome.ProjectDir(fixtureSourceProjectPath)))

	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "bad-manifest.zip")

	zipFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	zw := zip.NewWriter(zipFile)
	w, err := zw.Create("metadata.xml")
	require.NoError(t, err)
	_, err = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<cc-port><export><categories>` +
		`<category name="sessions" included="false"></category>` +
		`<category name="memory" included="false"></category>` +
		`<category name="history" included="false"></category>` +
		`<category name="file-history" included="false"></category>` +
		`<category name="config" included="false"></category>` +
		`<category name="todos" included="false"></category>` +
		`<category name="usage-data" included="false"></category>` +
		`<category name="plugins-data" included="false"></category>` +
		`<category name="tasks" included="false"></category>` +
		`<category name="bogus" included="true"></category>` +
		`</categories></export><placeholders></placeholders></cc-port>`))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, zipFile.Close())

	source, size := openArchive(t, archivePath)
	_, err = importer.Run(t.Context(), claudeHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: fixtureSourceProjectPath,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
}

func TestImport_HardFailsOnMissingManifestCategory(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	require.NoError(t, os.RemoveAll(claudeHome.ProjectDir(fixtureSourceProjectPath)))

	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "incomplete-manifest.zip")

	zipFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	zw := zip.NewWriter(zipFile)
	w, err := zw.Create("metadata.xml")
	require.NoError(t, err)
	_, err = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<cc-port><export><categories>` +
		`<category name="sessions" included="false"></category>` +
		`<category name="memory" included="false"></category>` +
		`<category name="history" included="false"></category>` +
		`<category name="file-history" included="false"></category>` +
		`<category name="config" included="false"></category>` +
		`</categories></export><placeholders></placeholders></cc-port>`))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, zipFile.Close())

	source, size := openArchive(t, archivePath)
	_, err = importer.Run(t.Context(), claudeHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: fixtureSourceProjectPath,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestImport_HardFailsOnUnknownEntryPrefix(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	// Remove the encoded project dir to avoid CheckConflict.
	require.NoError(t, os.RemoveAll(claudeHome.ProjectDir(fixtureSourceProjectPath)))

	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "rogue.zip")

	zipFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	zw := zip.NewWriter(zipFile)
	w, err := zw.Create("metadata.xml")
	require.NoError(t, err)
	_, err = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<cc-port><export><categories>` +
		`<category name="sessions" included="true"></category>` +
		`<category name="memory" included="false"></category>` +
		`<category name="history" included="false"></category>` +
		`<category name="file-history" included="false"></category>` +
		`<category name="config" included="false"></category>` +
		`<category name="todos" included="false"></category>` +
		`<category name="usage-data" included="false"></category>` +
		`<category name="plugins-data" included="false"></category>` +
		`<category name="tasks" included="false"></category>` +
		`</categories></export><placeholders></placeholders></cc-port>`))
	require.NoError(t, err)
	w, err = zw.Create("rogue/file.txt")
	require.NoError(t, err)
	_, err = w.Write([]byte("content"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, zipFile.Close())

	source, size := openArchive(t, archivePath)
	_, err = importer.Run(t.Context(), claudeHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: fixtureSourceProjectPath,
	})
	var entryErr *importer.UnknownArchiveEntryError
	require.ErrorAs(t, err, &entryErr)
	assert.Equal(t, "rogue/file.txt", entryErr.Name)
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

// buildMinimalSessionsArchive builds an archive with a sessions-only category
// set and the caller-supplied zip entries after metadata.xml. Each entry is
// stored uncompressed with the given name and content.
func buildMinimalSessionsArchive(t *testing.T, archivePath string, entries map[string][]byte) {
	t.Helper()

	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	defer func() { _ = archiveFile.Close() }()

	zipWriter := zip.NewWriter(archiveFile)
	defer func() { _ = zipWriter.Close() }()

	md := manifest.Metadata{
		Export: manifest.Info{
			Created:    time.Now().UTC(),
			Categories: manifest.BuildCategoryEntries(&manifest.CategorySet{Sessions: true}),
		},
	}
	data, err := xml.MarshalIndent(&md, "", "  ")
	require.NoError(t, err)
	mdEntry, err := zipWriter.Create("metadata.xml")
	require.NoError(t, err)
	_, err = mdEntry.Write(append([]byte(xml.Header), data...))
	require.NoError(t, err)

	for name, body := range entries {
		entry, err := zipWriter.Create(name)
		require.NoError(t, err)
		_, err = entry.Write(body)
		require.NoError(t, err)
	}
}

func TestRun_RejectsZipSlipEntry(t *testing.T) {
	destClaudeHome := buildEmptyDestClaudeHome(t)
	archivePath := filepath.Join(t.TempDir(), "slip.zip")
	buildMinimalSessionsArchive(t, archivePath, map[string][]byte{
		"sessions/../escape.txt": []byte("pwned"),
	})

	source, size := openArchive(t, archivePath)
	_, err := importer.Run(t.Context(), destClaudeHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: filepath.Join(t.TempDir(), "project"),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, importer.ErrZipSlip)

	escapeSibling := filepath.Join(destClaudeHome.Dir, "projects", "escape.txt")
	_, statErr := os.Stat(escapeSibling)
	assert.True(t, os.IsNotExist(statErr), "escape.txt must not land outside the staging base")
}

func TestRun_RejectsAbsoluteZipEntry(t *testing.T) {
	destClaudeHome := buildEmptyDestClaudeHome(t)
	archivePath := filepath.Join(t.TempDir(), "abs.zip")
	buildMinimalSessionsArchive(t, archivePath, map[string][]byte{
		"sessions//etc/bogus": []byte("x"),
	})

	source, size := openArchive(t, archivePath)
	_, err := importer.Run(t.Context(), destClaudeHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: filepath.Join(t.TempDir(), "project"),
	})
	require.Error(t, err)
}

// TestRun_RejectsArchiveWhenStagingBaseUnstageable plants a regular file at
// the file-history base path so the os.MkdirAll inside assertWithinRoot
// fails. This exercises the ErrStagingFailed branch (destination-side I/O
// failure on the staging jail) as distinct from the zip-slip containment
// branch (ErrZipSlip).
func TestRun_RejectsArchiveWhenStagingBaseUnstageable(t *testing.T) {
	destClaudeHome := buildEmptyDestClaudeHome(t)

	// Block the file-history base with a regular file so MkdirAll fails.
	require.NoError(t, os.WriteFile(
		destClaudeHome.FileHistoryDir(), []byte("blocker"), 0o600,
	))

	archivePath := filepath.Join(t.TempDir(), "fh.zip")
	buildArchiveWithFileHistoryEntry(t, archivePath,
		"file-history/abc/snapshot@v1", []byte("opaque"))

	source, size := openArchive(t, archivePath)
	_, err := importer.Run(t.Context(), destClaudeHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: filepath.Join(t.TempDir(), "project"),
	})

	require.Error(t, err)
	require.ErrorIs(t, err, importer.ErrStagingFailed)
}

// TestReadZipFile_RejectsOversizedEntry_SmallCap exercises the per-entry cap
// guard under a 1 MiB test override so the archive is a few megabytes rather
// than 600. The scale sibling in importer_large_test.go materializes a real
// 600 MiB archive and runs only under `-tags large`.
func TestReadZipFile_RejectsOversizedEntry_SmallCap(t *testing.T) {
	importer.SetMaxEntryBytes(t, 1<<20)

	destClaudeHome := buildEmptyDestClaudeHome(t)
	archivePath := filepath.Join(t.TempDir(), "bomb.zip")
	buildArchiveWithSingleEntry(t, archivePath, "sessions/bomb.json", 2<<20)

	source, size := openArchive(t, archivePath)
	_, err := importer.Run(t.Context(), destClaudeHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: filepath.Join(t.TempDir(), "project"),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, importer.ErrEntryCapExceeded)
}

// TestRun_RefusesArchiveExceedingAggregateUncompressedCap_SmallCap exercises
// the pass-1 aggregate-cap guard in classifyArchive under a 2 MiB test
// override, using three 1 MiB entries instead of multi-GiB payloads. The
// scale sibling in importer_large_test.go materializes the real multi-GiB
// archive and runs only under `-tags large`.
func TestRun_RefusesArchiveExceedingAggregateUncompressedCap_SmallCap(t *testing.T) {
	importer.SetMaxArchiveBytes(t, 2<<20)

	archivePath := buildArchiveWithAggregateSize(t, (2<<20)+1, 1<<20)
	destClaudeHome := buildEmptyDestClaudeHome(t)

	source, size := openArchive(t, archivePath)
	_, err := importer.Run(t.Context(), destClaudeHome, importer.Options{
		Source:      source,
		Size:        size,
		TargetPath:  filepath.Join(t.TempDir(), "project"),
		Resolutions: map[string]string{},
	})

	require.Error(t, err)
	require.ErrorIs(t, err, importer.ErrAggregateCapExceeded)
}

func TestRun_CancelsWhenContextCancelled(t *testing.T) {
	sourceClaudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "export.zip")
	buildTestArchive(t, sourceClaudeHome, archivePath)
	destClaudeHome := buildEmptyDestClaudeHome(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	source, size := openArchive(t, archivePath)
	_, err := importer.Run(ctx, destClaudeHome, importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: fixtureDestProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": fixtureDestProjectPath,
			"{{HOME}}":         filepath.Join(t.TempDir(), "home"),
		},
	})

	require.ErrorIs(t, err, context.Canceled)
}

// buildArchiveWithAggregateSize writes a ZIP at t.TempDir whose entries (after
// metadata.xml) sum to at least minAggregateBytes uncompressed, split into
// entries of size entrySize. Callers pick entrySize below the active per-entry
// cap so only the aggregate guard can reject the archive. Bodies are zero-
// filled: on-disk footprint stays small even for multi-GiB minAggregateBytes.
func buildArchiveWithAggregateSize(t *testing.T, minAggregateBytes, entrySize int64) string {
	t.Helper()

	archivePath := filepath.Join(t.TempDir(), "aggregate.zip")

	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	defer func() { _ = archiveFile.Close() }()

	zipWriter := zip.NewWriter(archiveFile)
	defer func() { _ = zipWriter.Close() }()

	writeMetadataXML(t, zipWriter)

	chunk := make([]byte, 1<<20) // 1 MiB of zeros, reused across writes
	var aggregateWritten int64
	for entryIndex := 0; aggregateWritten < minAggregateBytes; entryIndex++ {
		entryName := fmt.Sprintf("sessions/entry-%03d.bin", entryIndex)
		entry, err := zipWriter.Create(entryName)
		require.NoError(t, err)
		var written int64
		for written < entrySize {
			take := min(int64(len(chunk)), entrySize-written)
			_, err = entry.Write(chunk[:take])
			require.NoError(t, err)
			written += take
		}
		aggregateWritten += written
	}
	return archivePath
}

// buildArchiveWithSingleEntry writes a cc-port-shaped archive at archivePath
// with metadata.xml plus one entry named entryName filled with sizeBytes
// zeros. Zero-fill keeps the on-disk footprint in the low KiB regardless of
// sizeBytes, so tests that exercise the per-entry cap guard do not need
// gigabytes of real data.
func buildArchiveWithSingleEntry(t *testing.T, archivePath, entryName string, sizeBytes int64) {
	t.Helper()

	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	defer func() { _ = archiveFile.Close() }()

	zipWriter := zip.NewWriter(archiveFile)
	defer func() { _ = zipWriter.Close() }()

	writeMetadataXML(t, zipWriter)

	entry, err := zipWriter.Create(entryName)
	require.NoError(t, err)
	chunk := make([]byte, 1<<20)
	var written int64
	for written < sizeBytes {
		take := min(int64(len(chunk)), sizeBytes-written)
		_, err = entry.Write(chunk[:take])
		require.NoError(t, err)
		written += take
	}
}

// buildArchiveWithFileHistoryEntry writes a cc-port-shaped archive at
// archivePath with metadata.xml declaring the file-history category included
// and one entry at entryName carrying entryBody verbatim. Used by tests
// that need importer.Run to dispatch into the file-history staging path.
func buildArchiveWithFileHistoryEntry(
	t *testing.T, archivePath, entryName string, entryBody []byte,
) {
	t.Helper()

	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	defer func() { _ = archiveFile.Close() }()

	zipWriter := zip.NewWriter(archiveFile)
	defer func() { _ = zipWriter.Close() }()

	metadata := manifest.Metadata{
		Export: manifest.Info{
			Created: time.Now().UTC(),
			Categories: manifest.BuildCategoryEntries(&manifest.CategorySet{
				FileHistory: true,
			}),
		},
	}
	metadataBytes, err := xml.MarshalIndent(&metadata, "", "  ")
	require.NoError(t, err)
	metadataEntry, err := zipWriter.Create("metadata.xml")
	require.NoError(t, err)
	_, err = metadataEntry.Write(append([]byte(xml.Header), metadataBytes...))
	require.NoError(t, err)

	entry, err := zipWriter.Create(entryName)
	require.NoError(t, err)
	_, err = entry.Write(entryBody)
	require.NoError(t, err)
}

// writeMetadataXML emits the sessions-only metadata.xml header entry that
// every test archive needs for importer.Run to accept the archive shape.
func writeMetadataXML(t *testing.T, zipWriter *zip.Writer) {
	t.Helper()
	md := manifest.Metadata{
		Export: manifest.Info{
			Created:    time.Now().UTC(),
			Categories: manifest.BuildCategoryEntries(&manifest.CategorySet{Sessions: true}),
		},
	}
	mdBytes, err := xml.MarshalIndent(&md, "", "  ")
	require.NoError(t, err)
	mdEntry, err := zipWriter.Create("metadata.xml")
	require.NoError(t, err)
	_, err = mdEntry.Write(append([]byte(xml.Header), mdBytes...))
	require.NoError(t, err)
}

func TestRun_NilSource(t *testing.T) {
	home := testutil.SetupFixture(t)

	_, err := importer.Run(t.Context(), home, importer.Options{
		Source:     nil,
		Size:       0,
		TargetPath: "/Users/test/Projects/myproject",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "MaterializeStage",
		"error must hint at the missing pipeline stage to ease debugging")
}
