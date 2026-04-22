//go:build integration

package integration_test

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/testutil"
)

const (
	fixtureProjectPath = "/Users/test/Projects/myproject"
	fixtureHomeDir     = "/Users/test"

	destinationProjectPath = "/home/newuser/projects/cool-project"
	destinationHomeDir     = "/home/newuser"
)

// TestIntegration_MoveRoundTrip verifies a full dry-run + apply move cycle using real packages.
func TestIntegration_MoveRoundTrip(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	oldPath := fixtureProjectPath
	newPath := "/Users/test/Projects/renamed"

	// Dry run: verify replacements are detected.
	plan, err := move.DryRun(t.Context(), sourceHome, move.Options{
		OldPath:  oldPath,
		NewPath:  newPath,
		RefsOnly: true,
	})
	require.NoError(t, err)
	assert.Positive(t, plan.ReplacementsByCategory["history"], "dry run should detect history replacements")

	// Apply the move. RefsOnly=true avoids trying to copy a non-existent disk directory.
	err = move.Apply(t.Context(), sourceHome, move.Options{
		OldPath:  oldPath,
		NewPath:  newPath,
		RefsOnly: true,
	})
	require.NoError(t, err)

	// Old encoded project data dir should be gone.
	oldProjectDataDir := sourceHome.ProjectDir(oldPath)
	_, statErr := os.Stat(oldProjectDataDir)
	assert.True(t, os.IsNotExist(statErr), "old encoded project data dir should be removed after move")

	// New encoded project data dir should exist.
	newProjectDataDir := sourceHome.ProjectDir(newPath)
	assert.DirExists(t, newProjectDataDir, "new encoded project data dir should exist after move")

	// LocateProject on new path should succeed and have expected fields.
	locations, err := claude.LocateProject(sourceHome, newPath)
	require.NoError(t, err, "LocateProject should succeed for new project path")
	assert.True(t, locations.HasConfigBlock, "new project should have a config block")
}

// TestIntegration_ExportImportRoundTrip verifies a full export + import cycle using real packages.
func TestIntegration_ExportImportRoundTrip(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	archivePath := filepath.Join(t.TempDir(), "export.zip")

	runExportRoundTrip(t, sourceHome, archivePath)

	destinationHome := setupDestinationHome(t)

	importOptions := importer.Options{
		ArchivePath: archivePath,
		TargetPath:  destinationProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": destinationProjectPath,
			"{{HOME}}":         destinationHomeDir,
		},
	}

	err := importer.Run(t.Context(), destinationHome, importOptions)
	require.NoError(t, err, "import should succeed")

	verifyImportedProject(t, destinationHome, destinationProjectPath)
}

func runExportRoundTrip(t *testing.T, sourceHome *claude.Home, archivePath string) {
	t.Helper()

	trueVal := true
	exportOptions := export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  archivePath,
		Categories: manifest.CategorySet{
			Sessions:    true,
			Memory:      true,
			History:     true,
			FileHistory: true,
			Config:      true,
		},
		Placeholders: []manifest.Placeholder{
			{Key: "{{PROJECT_PATH}}", Original: fixtureProjectPath, Resolvable: &trueVal},
			{Key: "{{HOME}}", Original: fixtureHomeDir, Resolvable: &trueVal},
		},
	}

	_, err := export.Run(t.Context(), sourceHome, &exportOptions)
	require.NoError(t, err, "export should succeed")
	assert.FileExists(t, archivePath, "archive file should exist after export")

	verifyNoOriginalPathsInZip(t, archivePath)
}

func verifyNoOriginalPathsInZip(t *testing.T, archivePath string) {
	t.Helper()

	zipReader, err := zip.OpenReader(archivePath)
	require.NoError(t, err, "should be able to open exported archive")
	defer func() { _ = zipReader.Close() }()

	for _, zipFile := range zipReader.File {
		if zipFile.Name == "metadata.xml" {
			continue
		}
		// File-history snapshots are archived verbatim — their contents are
		// opaque user-file bytes, not anonymised material. A snapshot may
		// legitimately carry the original project path inside its body, and
		// cc-port preserves that by design.
		if strings.HasPrefix(zipFile.Name, "file-history/") {
			continue
		}
		readCloser, err := zipFile.Open()
		require.NoError(t, err, "should open zip entry %q", zipFile.Name)
		content, readErr := io.ReadAll(readCloser)
		_ = readCloser.Close()
		require.NoError(t, readErr, "should read zip entry %q", zipFile.Name)

		assert.NotContains(t, string(content), fixtureProjectPath,
			"file %q should not contain original project path", zipFile.Name)
		assert.NotContains(t, string(content), fixtureHomeDir,
			"file %q should not contain original home dir", zipFile.Name)
	}
}

func setupDestinationHome(t *testing.T) *claude.Home {
	t.Helper()

	destinationTempDir := t.TempDir()
	destinationClaudeDir := filepath.Join(destinationTempDir, "dotclaude")
	destinationConfigFile := filepath.Join(destinationTempDir, "dotclaude.json")

	require.NoError(t, os.MkdirAll(filepath.Join(destinationClaudeDir, "projects"), 0o750))
	require.NoError(t, os.WriteFile(destinationConfigFile, []byte(`{"projects":{}}`), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(destinationClaudeDir, "history.jsonl"),
		[]byte{},
		0o600,
	))

	return &claude.Home{
		Dir:        destinationClaudeDir,
		ConfigFile: destinationConfigFile,
	}
}

func verifyImportedProject(t *testing.T, destinationHome *claude.Home, destinationProjectPath string) {
	t.Helper()

	// LocateProject on target path should succeed.
	locations, err := claude.LocateProject(destinationHome, destinationProjectPath)
	require.NoError(t, err, "LocateProject should succeed on imported project")
	assert.NotEmpty(t, locations.SessionTranscripts,
		"imported project should have at least one session transcript")

	// Imported session transcripts should have no unresolved placeholders and
	// should contain the destination project path.
	for _, transcriptPath := range locations.SessionTranscripts {
		transcriptData, err := os.ReadFile(transcriptPath) //nolint:gosec // test-controlled path
		require.NoError(t, err)
		assert.NotContains(t, string(transcriptData), "{{PROJECT_PATH}}",
			"transcript %s should have no unresolved PROJECT_PATH placeholders", transcriptPath)
		assert.NotContains(t, string(transcriptData), "{{HOME}}",
			"transcript %s should have no unresolved HOME placeholders", transcriptPath)
	}

	// History file should have entries.
	historyData, err := os.ReadFile(destinationHome.HistoryFile())
	require.NoError(t, err)
	historyLines := strings.Split(strings.TrimSpace(string(historyData)), "\n")
	var historyEntryCount int
	for _, line := range historyLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry claude.HistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			historyEntryCount++
		}
	}
	assert.Positive(t, historyEntryCount, "imported history should have at least one entry")
}

// TestIntegration_ExportImport_ResolvableFalseRoundTrip verifies that a
// placeholder marked Resolvable: false at export time survives an import
// as literal `{{KEY}}` bytes when the caller does not supply a resolution
// for it. The pre-flight gate must allow the import to proceed; the token
// must land on disk unchanged in the destination.
func TestIntegration_ExportImport_ResolvableFalseRoundTrip(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	archivePath := filepath.Join(t.TempDir(), "export-unresolvable.zip")

	trueVal := true
	falseVal := false
	exportOptions := export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  archivePath,
		Categories: manifest.CategorySet{
			Sessions: true, Memory: true, History: true, FileHistory: true, Config: true,
		},
		Placeholders: []manifest.Placeholder{
			{Key: "{{PROJECT_PATH}}", Original: fixtureProjectPath, Resolvable: &trueVal},
			{Key: "{{HOME}}", Original: fixtureHomeDir, Resolvable: &trueVal},
			// Declare a placeholder whose literal occurrence we will inject
			// into the archive AFTER export — the sender acknowledges the
			// receiver has no mapping for this path.
			{
				Key:        "{{EXTERNAL_TOOL}}",
				Original:   "/opt/external-tool",
				Resolvable: &falseVal,
			},
		},
	}
	_, err := export.Run(t.Context(), sourceHome, &exportOptions)
	require.NoError(t, err)

	// Inject a literal {{EXTERNAL_TOOL}} into one of the archive's memory
	// bodies so the pre-flight gate sees it.
	injectTokenIntoMemoryEntry(t, archivePath, "memory/MEMORY.md", "{{EXTERNAL_TOOL}}")

	destinationHome := setupDestinationHome(t)

	// Supply ONLY PROJECT_PATH and HOME resolutions. EXTERNAL_TOOL is
	// deliberately omitted; the Resolvable: false manifest flag must be
	// what allows the import through.
	importOptions := importer.Options{
		ArchivePath: archivePath,
		TargetPath:  destinationProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": destinationProjectPath,
			"{{HOME}}":         destinationHomeDir,
		},
	}
	require.NoError(t, importer.Run(t.Context(), destinationHome, importOptions))

	// The literal {{EXTERNAL_TOOL}} must survive in the imported memory file.
	memoryPath := filepath.Join(
		destinationHome.ProjectDir(destinationProjectPath), "memory", "MEMORY.md",
	)
	data, err := os.ReadFile(memoryPath) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Contains(t, string(data), "{{EXTERNAL_TOOL}}",
		"Resolvable: false placeholder must survive import as literal {{KEY}}")
}

// injectTokenIntoMemoryEntry rewrites the zip at archivePath, appending
// the given token to the named memory entry. Used to test pre-flight
// classification of Resolvable: false placeholders whose occurrence was
// not placed there by export itself.
func injectTokenIntoMemoryEntry(t *testing.T, archivePath, entryName, token string) {
	t.Helper()

	reader, err := zip.OpenReader(archivePath)
	require.NoError(t, err)

	// Read existing entries into memory first, then rewrite the archive.
	type keptEntry struct {
		name    string
		content []byte
	}
	var kept []keptEntry
	for _, zipFile := range reader.File {
		rc, openErr := zipFile.Open()
		require.NoError(t, openErr)
		content, readErr := io.ReadAll(rc)
		_ = rc.Close()
		require.NoError(t, readErr)
		if zipFile.Name == entryName {
			content = append(content, '\n')
			content = append(content, []byte(token)...)
			content = append(content, '\n')
		}
		kept = append(kept, keptEntry{name: zipFile.Name, content: content})
	}
	_ = reader.Close()

	out, err := os.Create(archivePath) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	defer func() { _ = out.Close() }()
	writer := zip.NewWriter(out)
	for _, entry := range kept {
		w, createErr := writer.Create(entry.name)
		require.NoError(t, createErr)
		_, writeErr := w.Write(entry.content)
		require.NoError(t, writeErr)
	}
	require.NoError(t, writer.Close())
}

// TestIntegration_MoveRefsOnly verifies that a refs-only move updates ClaudeHome data
// without touching the actual project directory on disk.
func TestIntegration_MoveRefsOnly(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	oldPath := fixtureProjectPath
	newPath := "/Users/test/Projects/renamed-refsonly"

	err := move.Apply(t.Context(), sourceHome, move.Options{
		OldPath:  oldPath,
		NewPath:  newPath,
		RefsOnly: true,
	})
	require.NoError(t, err, "refs-only move should succeed")

	// New encoded project data dir should exist.
	newProjectDataDir := sourceHome.ProjectDir(newPath)
	assert.DirExists(t, newProjectDataDir, "new encoded project data dir should exist")

	// History should no longer reference the moved project. Use path-boundary
	// semantics: an unrelated project sharing the same prefix (e.g.
	// "myproject-extras") must remain untouched, so a raw substring check is
	// not valid here.
	historyData, err := os.ReadFile(sourceHome.HistoryFile())
	require.NoError(t, err)
	historyContent := string(historyData)
	assert.NotContains(t, historyContent, oldPath+"/",
		"history should not contain old path followed by /")
	assert.NotContains(t, historyContent, `"`+oldPath+`"`,
		"history should not contain old path as a quoted JSON value")
}

// TestIntegration_ExportImportRoundTrip_AllCategories verifies an end-to-end
// export + import round-trip with all 9 categories enabled. It asserts that
// every category lands in the destination and that path rewriting worked
// inside at least one session-keyed body (usage-data/session-meta).
func TestIntegration_ExportImportRoundTrip_AllCategories(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "export-all-categories.zip")

	trueVal := true
	_, err := export.Run(t.Context(), sourceHome, &export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  archivePath,
		Categories: manifest.CategorySet{
			Sessions:    true,
			Memory:      true,
			History:     true,
			FileHistory: true,
			Config:      true,
			Todos:       true,
			UsageData:   true,
			PluginsData: true,
			Tasks:       true,
		},
		Placeholders: []manifest.Placeholder{
			{Key: "{{PROJECT_PATH}}", Original: fixtureProjectPath, Resolvable: &trueVal},
			{Key: "{{HOME}}", Original: fixtureHomeDir, Resolvable: &trueVal},
		},
	})
	require.NoError(t, err, "export with all categories should succeed")

	assertArchiveHasAllCategoryPrefixes(t, archivePath)

	destinationHome := setupDestinationHome(t)

	err = importer.Run(t.Context(), destinationHome, importer.Options{
		ArchivePath: archivePath,
		TargetPath:  destinationProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": destinationProjectPath,
			"{{HOME}}":         destinationHomeDir,
		},
	})
	require.NoError(t, err, "import should succeed")

	imported, err := claude.LocateProject(destinationHome, destinationProjectPath)
	require.NoError(t, err, "LocateProject should succeed on imported project")
	assertAllCategoriesImported(t, imported)

	// Spot-check path rewriting in a session-keyed file that carries the project path.
	require.NotEmpty(t, imported.UsageDataSessionMeta)
	metaBody, err := os.ReadFile(imported.UsageDataSessionMeta[0])
	require.NoError(t, err)
	assert.Contains(t, string(metaBody), destinationProjectPath,
		"usage-data/session-meta body must carry the new project path")
	assert.NotContains(t, string(metaBody), fixtureProjectPath,
		"usage-data/session-meta body must not carry the original project path")
	assert.NotContains(t, string(metaBody), "{{PROJECT_PATH}}",
		"no unresolved placeholder tokens must remain in usage-data/session-meta")
}

func assertArchiveHasAllCategoryPrefixes(t *testing.T, archivePath string) {
	t.Helper()

	zipReader, err := zip.OpenReader(archivePath)
	require.NoError(t, err, "should be able to open exported archive")
	t.Cleanup(func() { _ = zipReader.Close() })

	var hasTodos, hasSessionMeta, hasFacets, hasPluginsData, hasTasks bool
	for _, f := range zipReader.File {
		switch {
		case strings.HasPrefix(f.Name, "todos/"):
			hasTodos = true
		case strings.HasPrefix(f.Name, "usage-data/session-meta/"):
			hasSessionMeta = true
		case strings.HasPrefix(f.Name, "usage-data/facets/"):
			hasFacets = true
		case strings.HasPrefix(f.Name, "plugins-data/"):
			hasPluginsData = true
		case strings.HasPrefix(f.Name, "tasks/"):
			hasTasks = true
		}
	}
	assert.True(t, hasTodos, "archive must include todos/ entries")
	assert.True(t, hasSessionMeta, "archive must include usage-data/session-meta/ entries")
	assert.True(t, hasFacets, "archive must include usage-data/facets/ entries")
	assert.True(t, hasPluginsData, "archive must include plugins-data/ entries")
	assert.True(t, hasTasks, "archive must include tasks/ entries")
}

func assertAllCategoriesImported(t *testing.T, imported *claude.ProjectLocations) {
	t.Helper()

	assert.NotEmpty(t, imported.SessionTranscripts, "sessions must be imported")
	assert.NotEmpty(t, imported.MemoryFiles, "memory must be imported")
	assert.Positive(t, imported.HistoryEntryCount, "history must be imported")
	assert.True(t, imported.HasConfigBlock, "config block must be imported")
	assert.NotEmpty(t, imported.FileHistoryDirs, "file-history must be imported")
	assert.NotEmpty(t, imported.TodoFiles, "todos must be imported")
	assert.NotEmpty(t, imported.UsageDataSessionMeta, "usage-data/session-meta must be imported")
	assert.NotEmpty(t, imported.UsageDataFacets, "usage-data/facets must be imported")
	assert.NotEmpty(t, imported.PluginsDataFiles, "plugins-data must be imported")
	assert.NotEmpty(t, imported.TaskFiles, "tasks must be imported")
}

// TestIntegration_ImportConflict verifies that importing back to a ClaudeHome where
// the same project path already exists produces an "already exists" error.
func TestIntegration_ImportConflict(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	archivePath := filepath.Join(t.TempDir(), "export-conflict.zip")

	trueVal := true
	exportOptions := export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  archivePath,
		Categories: manifest.CategorySet{
			Sessions: true,
			Memory:   true,
			History:  true,
			Config:   true,
		},
		Placeholders: []manifest.Placeholder{
			{Key: "{{PROJECT_PATH}}", Original: fixtureProjectPath, Resolvable: &trueVal},
			{Key: "{{HOME}}", Original: fixtureHomeDir, Resolvable: &trueVal},
		},
	}

	_, err := export.Run(t.Context(), sourceHome, &exportOptions)
	require.NoError(t, err, "export should succeed")

	// Try to import back to the same ClaudeHome at the same project path.
	importOptions := importer.Options{
		ArchivePath: archivePath,
		TargetPath:  fixtureProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": fixtureProjectPath,
			"{{HOME}}":         fixtureHomeDir,
		},
	}

	err = importer.Run(t.Context(), sourceHome, importOptions)
	require.Error(t, err, "import to existing project should fail with a conflict error")
	assert.Contains(t, err.Error(), "already exists",
		"conflict error message should mention 'already exists'")
}
