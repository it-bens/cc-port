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
	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/testutil"
)

const (
	fixtureProjectPath = "/Users/test/Projects/myproject"
	fixtureHomeDir     = "/Users/test"
)

// TestIntegration_MoveRoundTrip verifies a full dry-run + apply move cycle using real packages.
func TestIntegration_MoveRoundTrip(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	oldPath := fixtureProjectPath
	newPath := "/Users/test/Projects/renamed"

	// Dry run: verify replacements are detected.
	plan, err := move.DryRun(sourceHome, move.Options{
		OldPath:  oldPath,
		NewPath:  newPath,
		RefsOnly: true,
	})
	require.NoError(t, err)
	assert.Positive(t, plan.HistoryReplacements, "dry run should detect history replacements")

	// Apply the move. RefsOnly=true avoids trying to copy a non-existent disk directory.
	err = move.Apply(sourceHome, move.Options{
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

	destinationProjectPath := "/home/newuser/projects/cool-project"
	destinationHomeDir := "/home/newuser"

	importOptions := importer.Options{
		ArchivePath: archivePath,
		TargetPath:  destinationProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": destinationProjectPath,
			"{{HOME}}":         destinationHomeDir,
		},
	}

	err := importer.Run(destinationHome, importOptions)
	require.NoError(t, err, "import should succeed")

	verifyImportedProject(t, destinationHome, destinationProjectPath)
}

func runExportRoundTrip(t *testing.T, sourceHome *claude.Home, archivePath string) {
	t.Helper()

	trueVal := true
	exportOptions := export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  archivePath,
		Categories: export.CategorySet{
			Sessions:    true,
			Memory:      true,
			History:     true,
			FileHistory: true,
			Config:      true,
		},
		Placeholders: []export.Placeholder{
			{Key: "{{PROJECT_PATH}}", Original: fixtureProjectPath, Resolvable: &trueVal},
			{Key: "{{HOME}}", Original: fixtureHomeDir, Resolvable: &trueVal},
		},
	}

	err := export.Run(sourceHome, exportOptions)
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

	require.NoError(t, os.MkdirAll(filepath.Join(destinationClaudeDir, "projects"), 0750))
	require.NoError(t, os.WriteFile(destinationConfigFile, []byte(`{"projects":{}}`), 0600))
	require.NoError(t, os.WriteFile(
		filepath.Join(destinationClaudeDir, "history.jsonl"),
		[]byte{},
		0600,
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

// TestIntegration_MoveRefsOnly verifies that a refs-only move updates ClaudeHome data
// without touching the actual project directory on disk.
func TestIntegration_MoveRefsOnly(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	oldPath := fixtureProjectPath
	newPath := "/Users/test/Projects/renamed-refsonly"

	err := move.Apply(sourceHome, move.Options{
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

// TestIntegration_ImportConflict verifies that importing back to a ClaudeHome where
// the same project path already exists produces an "already exists" error.
func TestIntegration_ImportConflict(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	archivePath := filepath.Join(t.TempDir(), "export-conflict.zip")

	trueVal := true
	exportOptions := export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  archivePath,
		Categories: export.CategorySet{
			Sessions: true,
			Memory:   true,
			History:  true,
			Config:   true,
		},
		Placeholders: []export.Placeholder{
			{Key: "{{PROJECT_PATH}}", Original: fixtureProjectPath, Resolvable: &trueVal},
			{Key: "{{HOME}}", Original: fixtureHomeDir, Resolvable: &trueVal},
		},
	}

	err := export.Run(sourceHome, exportOptions)
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

	err = importer.Run(sourceHome, importOptions)
	require.Error(t, err, "import to existing project should fail with a conflict error")
	assert.Contains(t, err.Error(), "already exists",
		"conflict error message should mention 'already exists'")
}
