//go:build integration

package integration_test

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/file"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/tool/codex"
)

const (
	fixtureProjectPath = "/Users/test/Projects/myproject"
	fixtureHomeDir     = "/Users/test"

	destinationProjectPath = "/home/newuser/projects/cool-project"
	destinationHomeDir     = "/home/newuser"
)

func TestMain(m *testing.M) {
	restore, err := testutil.IsolateHome()
	if err != nil {
		panic(err)
	}
	exitCode := m.Run()
	restore()
	os.Exit(exitCode)
}

// targetsFor wraps home as the single-claude-tool target slice every
// generic command entry point (move, export, importer) now expects.
func targetsFor(home *claude.Home) []tool.Target {
	return []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
}

// toolSetFor returns a registry containing only the claude tool, matching
// targetsFor's single-tool slice.
func toolSetFor() *tool.Set { return tool.NewSet(claude.New()) }

// selectionFor builds a one-tool selection map from the named categories.
func selectionFor(categories ...string) map[string]map[string]bool {
	selected := make(map[string]bool, len(categories))
	for _, name := range categories {
		selected[name] = true
	}
	return map[string]map[string]bool{"claude": selected}
}

// allCategoriesSelection selects every category claude.New() declares.
func allCategoriesSelection() map[string]map[string]bool {
	claudeTool := claude.New()
	selected := make(map[string]bool, len(claudeTool.Categories()))
	for _, category := range claudeTool.Categories() {
		selected[category.Name] = true
	}
	return map[string]map[string]bool{"claude": selected}
}

// discoverPlaceholders mirrors what the cmd layer does before every export:
// call Workspace.Placeholders to discover the anchors actually referenced in
// the selected categories' content, so Export has something to anonymize.
// ImplicitAnchors (the import-side default-resolution mechanism) plays no
// part in this — export-side anonymization is driven entirely by the
// placeholders the caller supplies.
//
// Auto-discovery anchors {{HOME}} on the *real* test-runner's
// os.UserHomeDir(), which the fixture's fake "/Users/test" home never
// matches, so {{HOME}} never auto-discovers here. Every test in this file
// that asserts on {{HOME}} anonymization adds it explicitly, the same way a
// user hand-editing a manifest would when auto-discovery misses an anchor.
func discoverPlaceholders(
	t *testing.T, home *claude.Home, selection map[string]map[string]bool,
) map[string][]manifest.Placeholder {
	t.Helper()
	workspace := claude.NewWorkspace(home)
	placeholders, err := workspace.Placeholders(fixtureProjectPath, selection["claude"])
	require.NoError(t, err, "discover placeholders")
	placeholders = append(placeholders, manifest.Placeholder{Key: "{{HOME}}", Original: fixtureHomeDir})
	return map[string][]manifest.Placeholder{"claude": placeholders}
}

// findSurfaceCount returns the replacement count for surfaceName within
// the single claude ToolPlan in plan, failing the test if the tool block
// or the surface is absent.
func findSurfaceCount(t *testing.T, plan *move.Plan, surfaceName string) int {
	t.Helper()
	require.Len(t, plan.ByTool, 1)
	for _, surface := range plan.ByTool[0].Surfaces {
		if surface.Name == surfaceName {
			return surface.Count
		}
	}
	t.Fatalf("surface %q not present in plan", surfaceName)
	return 0
}

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

// runImport is a thin wrapper around importer.Run against a single-claude
// target, since {{PROJECT_PATH}} and {{HOME}} resolve automatically via
// the adapter's ImplicitAnchors and no longer need caller-supplied
// resolutions for the common case.
func runImport(t *testing.T, home *claude.Home, options *importer.Options) *importer.Result {
	t.Helper()
	result, err := importer.Run(t.Context(), toolSetFor(), targetsFor(home), options)
	require.NoError(t, err, "import should succeed")
	return result
}

// TestIntegration_MoveRoundTrip verifies a full dry-run + apply move cycle using real packages.
func TestIntegration_MoveRoundTrip(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)
	targets := targetsFor(sourceHome)

	oldPath := fixtureProjectPath
	newPath := "/Users/test/Projects/renamed"

	// Dry run: verify replacements are detected.
	plan, err := move.DryRun(t.Context(), targets, move.Options{
		OldPath:  oldPath,
		NewPath:  newPath,
		RefsOnly: true,
	})
	require.NoError(t, err)
	assert.Positive(t, findSurfaceCount(t, plan, "history"), "dry run should detect history replacements")

	// Apply the move. RefsOnly=true avoids trying to copy a non-existent disk directory.
	result, err := move.Apply(t.Context(), targets, move.Options{
		OldPath:  oldPath,
		NewPath:  newPath,
		RefsOnly: true,
	})
	require.NoError(t, err)
	require.False(t, result.Failed())

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

	source, size := openArchive(t, archivePath)
	runImport(t, destinationHome, &importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: destinationProjectPath,
	})

	verifyImportedProject(t, destinationHome, destinationProjectPath)
}

// TestIntegration_MultiToolArchiveExportsClaudeAndCodex covers the real CLI
// export path. Import is intentionally covered by importer tests: a Codex
// mutation run from this test process correctly sees the running Codex CLI in
// its process-table witness and must refuse the operation.
func TestIntegration_MultiToolArchiveExportsClaudeAndCodex(t *testing.T) {
	sharedProject := codex.FixtureProjectPath()
	claudeSource := testutil.SetupFixture(t)
	moveResult, err := move.Apply(t.Context(), targetsFor(claudeSource), move.Options{
		OldPath: fixtureProjectPath, NewPath: sharedProject, RefsOnly: true,
	})
	require.NoError(t, err)
	require.False(t, moveResult.Failed())
	codexSource := codex.SetupFixture(t)

	archivePath := filepath.Join(t.TempDir(), "multi-tool.zip")
	//nolint:gosec // G204: every argument is a test-controlled fixture or temporary path.
	command := exec.CommandContext(t.Context(), "go", "run", "./cmd/cc-port",
		"export", sharedProject,
		"--tool", "claude",
		"--tool", "codex",
		"--claude-home", claudeSource.Dir,
		"--codex-home", codexSource.Dir,
		"--all",
		"--output", archivePath,
	)
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "real CLI export failed:\n%s", output)
	assert.Contains(t, string(output), "Exported to "+archivePath)
	assertArchiveCarriesToolPrefixes(t, archivePath, "claude/", "codex/")
}

func assertArchiveCarriesToolPrefixes(t *testing.T, path string, prefixes ...string) {
	t.Helper()
	reader, err := zip.OpenReader(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()
	found := make(map[string]bool, len(prefixes))
	for _, file := range reader.File {
		for _, prefix := range prefixes {
			if strings.HasPrefix(file.Name, prefix) {
				found[prefix] = true
			}
		}
	}
	for _, prefix := range prefixes {
		assert.True(t, found[prefix], "archive must contain a %s entry", prefix)
	}
}

func runExportRoundTrip(t *testing.T, sourceHome *claude.Home, archivePath string) {
	t.Helper()

	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled tempdir path
	require.NoError(t, err, "create archive file")

	selection := selectionFor("sessions", "memory", "history", "file-history", "config")
	exportOptions := export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       archiveFile,
		Selected:     selection,
		Placeholders: discoverPlaceholders(t, sourceHome, selection),
	}

	_, err = export.Run(t.Context(), targetsFor(sourceHome), &exportOptions)
	require.NoError(t, err, "export should succeed")
	require.NoError(t, archiveFile.Close(), "close archive after export")
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
		if strings.HasPrefix(zipFile.Name, "claude/file-history/") {
			continue
		}
		readCloser, err := zipFile.Open()
		require.NoError(t, err, "should open zip entry %q", zipFile.Name)
		content, readErr := io.ReadAll(readCloser)
		_ = readCloser.Close()
		require.NoError(t, readErr, "should read zip entry %q", zipFile.Name)

		// Boundary-aware, not a plain substring check: the fixture's memory
		// notes legitimately mention a sibling project
		// (/Users/test/Projects/myproject-extras), and that sibling's real
		// path must survive anonymization untouched. A naive Contains would
		// misreport that correct behavior as a leak.
		assert.False(t, rewrite.ContainsBoundedPath(content, fixtureProjectPath),
			"file %q should not contain original project path", zipFile.Name)
		assert.False(t, rewrite.ContainsBoundedPath(content, fixtureHomeDir),
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

// TestIntegration_MoveRefsOnly verifies that a refs-only move updates ClaudeHome data
// without touching the actual project directory on disk.
func TestIntegration_MoveRefsOnly(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	oldPath := fixtureProjectPath
	newPath := "/Users/test/Projects/renamed-refsonly"

	result, err := move.Apply(t.Context(), targetsFor(sourceHome), move.Options{
		OldPath:  oldPath,
		NewPath:  newPath,
		RefsOnly: true,
	})
	require.NoError(t, err, "refs-only move should succeed")
	require.False(t, result.Failed())

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
// export + import round-trip with all categories enabled. It asserts that
// every category lands in the destination and that path rewriting worked
// inside at least one session-keyed body (usage-data/session-meta).
func TestIntegration_ExportImportRoundTrip_AllCategories(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "export-all-categories.zip")

	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled tempdir path
	require.NoError(t, err, "create archive file")

	allCategories := allCategoriesSelection()
	_, err = export.Run(t.Context(), targetsFor(sourceHome), &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       archiveFile,
		Selected:     allCategories,
		Placeholders: discoverPlaceholders(t, sourceHome, allCategories),
	})
	require.NoError(t, err, "export with all categories should succeed")
	require.NoError(t, archiveFile.Close(), "close archive after export")

	assertArchiveHasAllCategoryPrefixes(t, archivePath)

	destinationHome := setupDestinationHome(t)

	source, size := openArchive(t, archivePath)
	runImport(t, destinationHome, &importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: destinationProjectPath,
	})

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
		case strings.HasPrefix(f.Name, "claude/todos/"):
			hasTodos = true
		case strings.HasPrefix(f.Name, "claude/usage-data/session-meta/"):
			hasSessionMeta = true
		case strings.HasPrefix(f.Name, "claude/usage-data/facets/"):
			hasFacets = true
		case strings.HasPrefix(f.Name, "claude/plugins-data/"):
			hasPluginsData = true
		case strings.HasPrefix(f.Name, "claude/tasks/"):
			hasTasks = true
		}
	}
	assert.True(t, hasTodos, "archive must include claude/todos/ entries")
	assert.True(t, hasSessionMeta, "archive must include claude/usage-data/session-meta/ entries")
	assert.True(t, hasFacets, "archive must include claude/usage-data/facets/ entries")
	assert.True(t, hasPluginsData, "archive must include claude/plugins-data/ entries")
	assert.True(t, hasTasks, "archive must include claude/tasks/ entries")
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

// TestIntegration_ImportIsRerunnable verifies that importing back to a
// ClaudeHome where the same project path already exists succeeds and
// converges (no BC "already exists" refusal any more): import is
// idempotent, so a re-run into the same home is the documented recovery
// path for a partially failed import.
func TestIntegration_ImportIsRerunnable(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	archivePath := filepath.Join(t.TempDir(), "export-rerun.zip")

	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled tempdir path
	require.NoError(t, err, "create archive file")

	rerunSelection := selectionFor("sessions", "memory", "history", "config")
	exportOptions := export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       archiveFile,
		Selected:     rerunSelection,
		Placeholders: discoverPlaceholders(t, sourceHome, rerunSelection),
	}

	_, err = export.Run(t.Context(), targetsFor(sourceHome), &exportOptions)
	require.NoError(t, err, "export should succeed")
	require.NoError(t, archiveFile.Close(), "close archive after export")

	// Import back to the same ClaudeHome at the same project path, twice.
	for range 2 {
		source, size := openArchive(t, archivePath)
		runImport(t, sourceHome, &importer.Options{
			Source:     source,
			Size:       size,
			TargetPath: fixtureProjectPath,
		})
	}

	historyBytes, err := os.ReadFile(sourceHome.HistoryFile())
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(historyBytes), "\n"), "\n")
	seen := make(map[string]int)
	for _, line := range lines {
		seen[line]++
	}
	for line, count := range seen {
		assert.Equalf(t, 1, count, "history line must not be duplicated by a re-import: %s", line)
	}
}

func TestIntegration_EncryptedExportImportRoundTrip(t *testing.T) {
	const passphrase = "round-trip-passphrase"
	sourceHome := testutil.SetupFixture(t)

	sid := stageFixtureWorkflowTree(t, sourceHome)

	archivePath := filepath.Join(t.TempDir(), "export.zip.age")

	// Build an encrypted archive via pipeline composition.
	writeStages := []pipeline.WriterStage{
		&encrypt.WriterStage{Pass: passphrase},
		&file.Sink{Path: archivePath},
	}
	writer, err := pipeline.RunWriter(t.Context(), writeStages)
	require.NoError(t, err)

	encryptedSelection := selectionFor("sessions", "memory", "history", "file-history", "config")
	exportOptions := export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       writer,
		Selected:     encryptedSelection,
		Placeholders: discoverPlaceholders(t, sourceHome, encryptedSelection),
	}
	_, err = export.Run(t.Context(), targetsFor(sourceHome), &exportOptions)
	require.NoError(t, err, "encrypted export should succeed")
	require.NoError(t, writer.Close(), "writer Close should flush age trailer and close file sink")

	// Output begins with age magic bytes.
	headerBytes, err := os.ReadFile(archivePath) //nolint:gosec // G304: test-controlled tempdir path
	require.NoError(t, err)
	require.True(t, encrypt.IsEncrypted(headerBytes), "encrypted archive should match age magic-byte prefix")

	// Read without passphrase rejects via the stage's Strict matrix.
	_, dispatchErr := pipeline.RunReader(t.Context(), []pipeline.ReaderStage{
		&file.Source{Path: archivePath},
		&encrypt.ReaderStage{Pass: "", Mode: encrypt.Strict},
	})
	require.ErrorIs(t, dispatchErr, encrypt.ErrPassphraseRequired)
	// RunReader closed the accumulated file-source closer on the mismatch path.

	// Read with passphrase round-trips through importer.Run.
	source, err := pipeline.RunReader(t.Context(), []pipeline.ReaderStage{
		&file.Source{Path: archivePath},
		&encrypt.ReaderStage{Pass: passphrase, Mode: encrypt.Strict},
		&pipeline.MaterializeStage{},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = source.Close() })

	destinationHome := setupDestinationHome(t)
	runImport(t, destinationHome, &importer.Options{
		Source:     source.ReaderAt,
		Size:       source.Size,
		TargetPath: destinationProjectPath,
	})

	verifyImportedProject(t, destinationHome, destinationProjectPath)

	recordPath := filepath.Join(
		destinationHome.ProjectDir(destinationProjectPath), sid, "workflows", "wf_ccport.json",
	)
	recordBody, err := os.ReadFile(recordPath) //nolint:gosec // test-controlled path
	require.NoError(t, err, "read imported workflow run record")
	assert.Contains(t, string(recordBody), filepath.Base(destinationHome.ProjectDir(destinationProjectPath)),
		"scriptPath must carry the recipient's encoded dir after an encrypted round-trip")
	assert.NotContains(t, string(recordBody), filepath.Base(sourceHome.ProjectDir(fixtureProjectPath)),
		"scriptPath must not retain the sender's encoded dir")
}

// firstFileUnder returns the first non-directory descendant of root, in walk
// order. Used by the mtime round-trip test to pick a representative source
// file under file-history/ and todos/ without hard-coding a fixture-specific
// filename.
func firstFileUnder(t *testing.T, root string) string {
	t.Helper()
	var found string
	require.NoError(t,
		filepath.WalkDir(root, func(path string, dirEntry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !dirEntry.IsDir() && found == "" {
				found = path
			}
			return nil
		}),
		"walk %s", root,
	)
	require.NotEmpty(t, found, "fixture must include at least one file under %s", root)
	return found
}

// assertImportedMtime stats path and asserts its mtime is within one second of
// want. One-second tolerance matches the Info-ZIP extTimeExtraID (0x5455) field
// archive/zip writes — that extra field carries whole-second precision.
func assertImportedMtime(t *testing.T, path string, want time.Time, label string) {
	t.Helper()
	stat, err := os.Stat(path)
	require.NoError(t, err, "stat %s", label)
	assert.WithinDuration(t, want, stat.ModTime(), time.Second,
		"%s should carry source mtime", label)
}

// TestIntegration_ExportImportRoundTrip_PreservesMtime stamps known mtimes on
// one representative source file per in-scope verbatim staging path
// (session JSONL, memory file, file-history snapshot, todos entry), runs an
// export → import round-trip into a fresh target, and asserts the mtimes
// survive promotion. The merge-result files (history.jsonl, .claude.json) are
// asserted not to carry source mtimes — their mtime reflects import time.
func TestIntegration_ExportImportRoundTrip_PreservesMtime(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)
	encodedSourceProjectDir := sourceHome.ProjectDir(fixtureProjectPath)

	// Pin known mtimes on one representative source file per in-scope
	// staging path. Each instant is unique within the test so a regression
	// where one file's mtime overwrites another's fails with a clear
	// assertion. Whole-second precision matches the Info-ZIP extTimeExtraID
	// (0x5455) extra field that archive/zip writes.
	sessionMtime := time.Date(2025, 5, 1, 10, 0, 0, 0, time.UTC)
	memoryMtime := time.Date(2025, 5, 2, 11, 0, 0, 0, time.UTC)
	fileHistoryMtime := time.Date(2025, 5, 3, 12, 0, 0, 0, time.UTC)
	todosMtime := time.Date(2025, 5, 4, 13, 0, 0, 0, time.UTC)

	// fixtureSessionFile is a session JSONL the testutil fixture stages
	// under the encoded project dir. Use its real filename so the source
	// file the export reads — and the destination file we re-stat — agree.
	const fixtureSessionFile = "a1b2c3d4-0000-0000-0000-000000000001.jsonl"

	sessionPath := filepath.Join(encodedSourceProjectDir, fixtureSessionFile)
	require.NoError(t, os.Chtimes(sessionPath, sessionMtime, sessionMtime),
		"set source session mtime")

	memoryPath := filepath.Join(encodedSourceProjectDir, "memory", "MEMORY.md")
	require.NoError(t, os.Chtimes(memoryPath, memoryMtime, memoryMtime),
		"set source memory mtime")

	fileHistoryRoot := sourceHome.FileHistoryDir()
	snapshotPath := firstFileUnder(t, fileHistoryRoot)
	require.NoError(t, os.Chtimes(snapshotPath, fileHistoryMtime, fileHistoryMtime),
		"set source file-history mtime")

	// Stamping a todos source file exercises the session-keyed staging path end-to-end.
	todosRoot := sourceHome.TodosDir()
	todosPath := firstFileUnder(t, todosRoot)
	require.NoError(t, os.Chtimes(todosPath, todosMtime, todosMtime),
		"set source todos mtime")

	roundTripStartedAt := time.Now()

	archivePath := filepath.Join(t.TempDir(), "export.zip")
	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled tempdir path
	require.NoError(t, err, "create archive file")

	mtimeSelection := selectionFor("sessions", "memory", "history", "file-history", "config", "todos")
	_, err = export.Run(t.Context(), targetsFor(sourceHome), &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       archiveFile,
		Selected:     mtimeSelection,
		Placeholders: discoverPlaceholders(t, sourceHome, mtimeSelection),
	})
	require.NoError(t, err, "export should succeed")
	require.NoError(t, archiveFile.Close(), "close archive after export")

	destinationHome := setupDestinationHome(t)
	source, size := openArchive(t, archivePath)
	runImport(t, destinationHome, &importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: destinationProjectPath,
	})

	// Positive cases — verbatim entries carry source mtime through promotion.
	encodedDestProjectDir := destinationHome.ProjectDir(destinationProjectPath)

	assertImportedMtime(t,
		filepath.Join(encodedDestProjectDir, fixtureSessionFile),
		sessionMtime, "session JSONL")
	assertImportedMtime(t,
		filepath.Join(encodedDestProjectDir, "memory", "MEMORY.md"),
		memoryMtime, "memory file")
	assertImportedMtime(t,
		filepath.Join(destinationHome.FileHistoryDir(),
			strings.TrimPrefix(snapshotPath, fileHistoryRoot+string(filepath.Separator))),
		fileHistoryMtime, "file-history snapshot")
	assertImportedMtime(t,
		filepath.Join(destinationHome.TodosDir(),
			strings.TrimPrefix(todosPath, todosRoot+string(filepath.Separator))),
		todosMtime, "todos entry")

	// Negative cases — merged/synth files reflect import time, not source mtime.
	historyStat, err := os.Stat(destinationHome.HistoryFile())
	require.NoError(t, err, "stat imported history.jsonl")
	assert.False(t, historyStat.ModTime().Before(roundTripStartedAt),
		"history.jsonl is a merge result; its mtime should be at or after round-trip start")

	configStat, err := os.Stat(destinationHome.ConfigFile)
	require.NoError(t, err, "stat imported .claude.json")
	assert.False(t, configStat.ModTime().Before(roundTripStartedAt),
		".claude.json is a merge result; its mtime should be at or after round-trip start")
}

// stageWorkflowTree writes a workflow run record, its script, and a subagent
// workflow transcript under sessionSubdir, mirroring the on-disk shape the
// Claude Code Workflow tool produces (projects/<encoded>/<sid>/workflows/**
// and .../subagents/workflows/**). The run record's scriptPath embeds
// encodedProjectDir (the absolute ~/.claude/projects/<encoded> directory), and
// the args/result/transcript embed projectPath, so both the {{PROJECT_DIR}}
// and {{PROJECT_PATH}} rewrites are observable.
func stageWorkflowTree(t *testing.T, sessionSubdir, encodedProjectDir, projectPath string) {
	t.Helper()
	sid := filepath.Base(sessionSubdir)

	files := map[string][]byte{
		filepath.Join(sessionSubdir, "workflows", "wf_ccport.json"): []byte(fmt.Sprintf(
			`{"runId":"wf_ccport","workflowName":"review-changeset",`+
				`"scriptPath":"%s/%s/workflows/scripts/review-changeset-wf_ccport.js",`+
				`"args":{"kind":"files","target":"%s/internal/parser"},`+
				`"result":"Reviewed %s/internal/parser and reported findings.","status":"completed"}`,
			encodedProjectDir, sid, projectPath, projectPath)),
		filepath.Join(sessionSubdir, "workflows", "scripts", "review-changeset-wf_ccport.js"): []byte(fmt.Sprintf(
			"// review-changeset workflow scoped to %s/\nexport const meta = { name: 'review-changeset' }\n",
			projectPath)),
		filepath.Join(sessionSubdir, "subagents", "workflows", "wf_ccport", "agent-review.jsonl"): []byte(fmt.Sprintf(
			`{"type":"system","subtype":"workflow_agent_start","cwd":"%s","sessionId":"%s"}`+"\n"+
				`{"type":"human","message":{"role":"user","content":"Review %s/internal/render"}}`+"\n",
			projectPath, sid, projectPath)),
	}

	for path, body := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750), "create workflow dir for %s", path)
		require.NoError(t, os.WriteFile(path, body, 0o600), "write workflow fixture %s", path)
	}
}

// stageFixtureWorkflowTree stages the canonical fixture's workflow tree under
// its first session subdir and returns the session UUID that names it.
func stageFixtureWorkflowTree(t *testing.T, home *claude.Home) string {
	t.Helper()
	locations, err := claude.LocateProject(home, fixtureProjectPath)
	require.NoError(t, err, "locate fixture project")
	require.NotEmpty(t, locations.SessionSubdirs, "fixture must have a session subdir to host workflows")
	sessionSubdir := locations.SessionSubdirs[0]
	stageWorkflowTree(t, sessionSubdir, home.ProjectDir(fixtureProjectPath), fixtureProjectPath)
	return filepath.Base(sessionSubdir)
}

// TestIntegration_ExportImportRoundTrip_Workflows verifies that a workflow tree
// living inside a session subdir (projects/<encoded>/<sid>/workflows/** and
// .../subagents/workflows/**) survives an export + import round-trip with its
// project-path references rewritten to the destination. Workflows are not a
// dedicated category: they ride the sessions category through the sessions/
// staging path on both export and import.
func TestIntegration_ExportImportRoundTrip_Workflows(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	sid := stageFixtureWorkflowTree(t, sourceHome)

	archivePath := filepath.Join(t.TempDir(), "export-workflows.zip")
	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled tempdir path
	require.NoError(t, err, "create archive file")
	workflowSelection := selectionFor("sessions")
	_, err = export.Run(t.Context(), targetsFor(sourceHome), &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       archiveFile,
		Selected:     workflowSelection,
		Placeholders: discoverPlaceholders(t, sourceHome, workflowSelection),
	})
	require.NoError(t, err, "export with sessions category should succeed")
	require.NoError(t, archiveFile.Close(), "close archive after export")

	destinationHome := setupDestinationHome(t)
	source, size := openArchive(t, archivePath)
	runImport(t, destinationHome, &importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: destinationProjectPath,
	})

	destProjectDir := destinationHome.ProjectDir(destinationProjectPath)
	recordPath := filepath.Join(destProjectDir, sid, "workflows", "wf_ccport.json")
	scriptPath := filepath.Join(destProjectDir, sid, "workflows", "scripts", "review-changeset-wf_ccport.js")
	transcriptPath := filepath.Join(destProjectDir, sid, "subagents", "workflows", "wf_ccport", "agent-review.jsonl")

	// The script carries no project path, so only its survival matters; the
	// run record and transcript are read below, which proves theirs survived.
	assert.FileExists(t, scriptPath, "workflow script should survive the round-trip")

	recordBody, err := os.ReadFile(recordPath) //nolint:gosec // test-controlled path
	require.NoError(t, err, "read imported workflow run record")
	assert.Contains(t, string(recordBody), destinationProjectPath,
		"run record must carry the destination project path after import")
	assert.NotContains(t, string(recordBody), fixtureProjectPath,
		"run record must not retain the source project path")
	assert.NotContains(t, string(recordBody), "{{PROJECT_PATH}}",
		"run record must have no unresolved placeholders")
	assert.NotContains(t, string(recordBody), "{{HOME}}",
		"run record must have no unresolved placeholders")

	senderEncodedDir := filepath.Base(sourceHome.ProjectDir(fixtureProjectPath))
	recipientEncodedDir := filepath.Base(destinationHome.ProjectDir(destinationProjectPath))
	assert.Contains(t, string(recordBody), recipientEncodedDir,
		"scriptPath must carry the recipient's encoded dir after import")
	assert.NotContains(t, string(recordBody), senderEncodedDir,
		"scriptPath must not retain the sender's encoded dir")

	transcriptBody, err := os.ReadFile(transcriptPath) //nolint:gosec // test-controlled path
	require.NoError(t, err, "read imported subagent workflow transcript")
	assert.Contains(t, string(transcriptBody), destinationProjectPath,
		"subagent workflow transcript must carry the destination project path")
	assert.NotContains(t, string(transcriptBody), fixtureProjectPath,
		"subagent workflow transcript must not retain the source project path")
}

// TestIntegration_MoveRewritesWorkflowTree verifies that move rewrites the
// project-path references inside a session subdir's workflow tree when
// DeepRewrite is enabled — the same opt-in gate that covers subagent
// transcripts. It also rewrites the encoded storage-dir segment (e.g. a run
// record's scriptPath) from the old encoded dir to the new one.
func TestIntegration_MoveRewritesWorkflowTree(t *testing.T) {
	sourceHome := testutil.SetupFixture(t)

	sid := stageFixtureWorkflowTree(t, sourceHome)

	newPath := "/Users/test/Projects/renamed-workflows"
	result, err := move.Apply(t.Context(), targetsFor(sourceHome), move.Options{
		OldPath:     fixtureProjectPath,
		NewPath:     newPath,
		RefsOnly:    true,
		DeepRewrite: true,
	})
	require.NoError(t, err, "move with transcript rewrite should succeed")
	require.False(t, result.Failed())

	recordPath := filepath.Join(sourceHome.ProjectDir(newPath), sid, "workflows", "wf_ccport.json")
	require.FileExists(t, recordPath, "workflow run record should move with the project dir")

	recordBody, err := os.ReadFile(recordPath) //nolint:gosec // test-controlled path
	require.NoError(t, err, "read moved workflow run record")
	assert.Contains(t, string(recordBody), newPath,
		"moved run record must carry the new project path")
	assert.NotContains(t, string(recordBody), fixtureProjectPath,
		"moved run record must not retain the source project path")
	assert.Contains(t, string(recordBody), sourceHome.ProjectDir(newPath),
		"moved run record scriptPath must carry the new encoded dir")
	assert.NotContains(t, string(recordBody), sourceHome.ProjectDir(fixtureProjectPath),
		"moved run record scriptPath must not retain the old encoded dir")
}
