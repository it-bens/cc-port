package codex

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	portexport "github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/tool"
)

const (
	fixtureThreadOne = "00000000-0000-4000-8000-000000000001"
	fixtureThreadTwo = "00000000-0000-4000-8000-000000000002"
)

func TestExportDecompressesRolloutsAndReportsEraA(t *testing.T) {
	home := SetupFixture(t)
	compressedPath := filepath.Join(
		home.Dir, sessionsSubdir, "2026", "07", "18", "rollout-compressed.jsonl"+zstSuffix,
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(compressedPath), 0o750))
	line := `{"type":"session_meta","payload":{"session_id":"compressed-thread","cwd":"` +
		FixtureProjectPath() + `"}}` + "\n"
	compressed, err := compressZstd([]byte(line))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(compressedPath, compressed, 0o600))

	workspace := newWorkspace(home, func(string) string { return "" }, nil, nil, nil)
	var archiveBytes bytes.Buffer
	writer := zip.NewWriter(&archiveBytes)
	sink := archive.NewSink(writer, toolName, nil)
	result, err := workspace.Export(context.Background(), FixtureProjectPath(), map[string]bool{categorySessions: true}, sink)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	assert.Contains(t, result.Skipped, rolloutFixturePath(home, eraAPath))
	require.Len(t, result.Warnings, 1)
	reader, err := zip.NewReader(bytes.NewReader(archiveBytes.Bytes()), int64(archiveBytes.Len()))
	require.NoError(t, err)
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	assert.Contains(t, names, "codex/sessions/2026/07/18/rollout-compressed.jsonl")
	assert.NotContains(t, names, "codex/sessions/2026/07/18/rollout-compressed.jsonl.zst")
	for _, file := range reader.File {
		if file.Name != "codex/sessions/2026/07/18/rollout-compressed.jsonl" {
			continue
		}
		body, err := file.Open()
		require.NoError(t, err)
		decompressed, err := io.ReadAll(body)
		require.NoError(t, err)
		require.NoError(t, body.Close())
		assert.Equal(t, line, string(decompressed))
	}
}

func TestExportHistoryOnlyIncludesAssociatedHistoryLines(t *testing.T) {
	home := SetupFixture(t)
	expected, _ := writeRoundTripLineStores(t, home)
	history := append(append([]byte(nil), expected...), []byte(`{"session_id":"unrelated-thread","ts":300,"text":"other"}`+"\n")...)
	require.NoError(t, os.WriteFile(filepath.Join(home.Dir, codexHistoryFile), history, 0o600))

	workspace := quietTestWorkspace(home)
	var archiveBytes bytes.Buffer
	writer := zip.NewWriter(&archiveBytes)
	sink := archive.NewSink(writer, toolName, nil)
	_, err := workspace.Export(t.Context(), FixtureProjectPath(), map[string]bool{categoryHistory: true}, sink)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	reader, err := zip.NewReader(bytes.NewReader(archiveBytes.Bytes()), int64(archiveBytes.Len()))
	require.NoError(t, err)
	for _, file := range reader.File {
		if file.Name != "codex/history/"+codexHistoryFile {
			continue
		}
		body, err := file.Open()
		require.NoError(t, err)
		actual, err := io.ReadAll(body)
		require.NoError(t, err)
		require.NoError(t, body.Close())
		assert.Equal(t, expected, actual)
		return
	}
	t.Fatal("history entry was not exported")
}

func TestStageRejectsUnknownCodexRelativeEntryWithoutStaging(t *testing.T) {
	home := SetupFixture(t)
	var archiveBytes bytes.Buffer
	writer := zip.NewWriter(&archiveBytes)
	entryWriter, err := writer.Create("codex/unknown.json")
	require.NoError(t, err)
	_, err = entryWriter.Write([]byte("payload"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	reader, err := archive.OpenReader(bytes.NewReader(archiveBytes.Bytes()), int64(archiveBytes.Len()))
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)
	require.Len(t, entries, 1)

	workspace := quietTestWorkspace(home)
	staged, err := workspace.Stage(t.Context(), FixtureProjectPath(), entries[0].Entry, nil)

	var unknown *UnknownArchiveEntryError
	if !errors.As(err, &unknown) {
		t.Fatalf("Stage() error = %v, want UnknownArchiveEntryError", err)
	}
	assert.Equal(t, "unknown.json", unknown.Name)
	assert.Empty(t, staged)
	assert.Empty(t, workspace.historyAppends)
	assert.Empty(t, workspace.indexAppends)
	assert.Empty(t, workspace.sidecarAppends)
}

func TestCodexRoundTripRestoresRolloutsHistoryAndSessionIndex(t *testing.T) {
	sourceHome := SetupFixture(t)
	history, index := writeRoundTripLineStores(t, sourceHome)
	archiveBytes := exportFixtureArchive(t, sourceHome)
	destinationHome, _ := setupImportDestination(t)

	result := importFixtureArchive(t, archiveBytes, destinationHome)

	assert.Empty(t, result.Warnings)
	assertImportedRolloutMatches(t, sourceHome, destinationHome, eraCPath)
	assertImportedRolloutMatches(t, sourceHome, destinationHome, eraBPath)
	archived := "archived_sessions/rollout-2026-07-10T09-00-00-00000000-0000-4000-8000-000000000002.jsonl"
	assertImportedRolloutMatches(t, sourceHome, destinationHome, archived)
	assertFileBytes(t, filepath.Join(destinationHome.Dir, codexHistoryFile), history)
	assertFileBytes(t, filepath.Join(destinationHome.Dir, sessionIndexFile), index)
}

func TestCodexImportLeavesConfigTOMLByteIdentical(t *testing.T) {
	sourceHome := SetupFixture(t)
	writeRoundTripLineStores(t, sourceHome)
	archiveBytes := exportFixtureArchive(t, sourceHome)
	destinationHome, config := setupImportDestination(t)

	importFixtureArchive(t, archiveBytes, destinationHome)

	assertFileBytes(t, filepath.Join(destinationHome.Dir, configTOMLFileName), config)
}

func TestCodexImportRerunDoesNotDuplicateHistoryOrSessionIndex(t *testing.T) {
	sourceHome := SetupFixture(t)
	history, index := writeRoundTripLineStores(t, sourceHome)
	archiveBytes := exportFixtureArchive(t, sourceHome)
	destinationHome, _ := setupImportDestination(t)

	importFixtureArchive(t, archiveBytes, destinationHome)
	importFixtureArchive(t, archiveBytes, destinationHome)

	assertFileBytes(t, filepath.Join(destinationHome.Dir, codexHistoryFile), history)
	assertFileBytes(t, filepath.Join(destinationHome.Dir, sessionIndexFile), index)
}

func TestCodexSidecarRerunAppliesThreadCreatedAfterFirstImport(t *testing.T) {
	sourceHome := SetupFixture(t)
	writeRoundTripLineStores(t, sourceHome)
	insertThreadRow(t, filepath.Join(sourceHome.SQLiteDir, stateDBFileName), fixtureThreadTwo, threadRowMetadata{
		Title: "archived fixture", ArchivedAt: 1_752_137_200, GitSHA: "deadbeef",
		GitBranch: "main", GitOriginURL: "https://example.invalid/fixture.git",
	})
	archiveBytes := exportFixtureArchive(t, sourceHome)
	destinationHome, _ := setupImportDestination(t)

	first := importFixtureArchive(t, archiveBytes, destinationHome)

	require.Equal(t, []string{
		"1 threads sidecar row(s) could not be applied because Codex has not created their thread rows yet; " +
			"rerun import after opening the project",
	}, first.Warnings[toolName])
	insertThreadRow(t, filepath.Join(destinationHome.SQLiteDir, stateDBFileName), fixtureThreadTwo, threadRowMetadata{})

	second := importFixtureArchive(t, archiveBytes, destinationHome)

	assert.Empty(t, second.Warnings)
	assertThreadMetadata(t, filepath.Join(destinationHome.SQLiteDir, stateDBFileName), fixtureThreadTwo, threadRowMetadata{
		Title: "archived fixture", ArchivedAt: 1_752_137_200, GitSHA: "deadbeef",
		GitBranch: "main", GitOriginURL: "https://example.invalid/fixture.git",
	})
}

func writeRoundTripLineStores(t *testing.T, home *Home) (history, index []byte) {
	t.Helper()
	history = []byte(
		`{"session_id":"` + fixtureThreadOne + `","ts":100,"text":"first"}` + "\n" +
			`{"session_id":"` + fixtureThreadTwo + `","ts":200,"text":"archived"}` + "\n",
	)
	index = []byte(
		`{"id":"` + fixtureThreadOne + `","thread_name":"first","updated_at":"2026-07-17T10:00:00Z"}` + "\n" +
			`{"id":"` + fixtureThreadTwo + `","thread_name":"archived","updated_at":"2026-07-10T09:00:00Z"}` + "\n",
	)
	require.NoError(t, os.WriteFile(filepath.Join(home.Dir, codexHistoryFile), history, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home.Dir, sessionIndexFile), index, 0o600))
	return history, index
}

func exportFixtureArchive(t *testing.T, home *Home) []byte {
	t.Helper()
	workspace := quietTestWorkspace(home)
	selected := map[string]bool{categorySessions: true, categoryHistory: true}
	placeholders, err := workspace.Placeholders(FixtureProjectPath(), selected)
	require.NoError(t, err)
	adapter := New()
	var output bytes.Buffer
	_, err = portexport.Run(t.Context(), []tool.Target{{Tool: adapter, Workspace: workspace}}, &portexport.Options{
		ProjectPath: FixtureProjectPath(), Output: &output,
		Selected:     map[string]map[string]bool{toolName: selected},
		Placeholders: map[string][]manifest.Placeholder{toolName: placeholders},
	})
	require.NoError(t, err)
	return output.Bytes()
}

func importFixtureArchive(t *testing.T, data []byte, home *Home) *importer.Result {
	t.Helper()
	adapter := New()
	set := tool.NewSet(adapter)
	reader := bytes.NewReader(data)
	result, err := importer.Run(t.Context(), set, []tool.Target{{Tool: adapter, Workspace: quietTestWorkspace(home)}}, &importer.Options{
		Source: reader, Size: int64(reader.Len()), TargetPath: FixtureProjectPath(),
	})
	require.NoError(t, err)
	return result
}

func quietTestWorkspace(home *Home) *Workspace {
	return newWorkspace(
		home, func(string) string { return "" }, func() ([]ProcessInfo, error) { return nil, nil },
		func() time.Time { return time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC) }, func(int) bool { return false },
	)
}

func setupImportDestination(t *testing.T) (home *Home, config []byte) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dotcodex")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	config = []byte("# recipient trust stays local\n[projects.\"/recipient/only\"]\ntrust_level = \"trusted\"\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, configTOMLFileName), config, 0o600))
	buildFixtureStateDB(t, filepath.Join(dir, stateDBFileName))
	return &Home{Dir: dir, SQLiteDir: dir}, config
}

func assertImportedRolloutMatches(t *testing.T, source, destination *Home, relative string) {
	t.Helper()
	sourcePath := filepath.Join(source.Dir, filepath.FromSlash(relative))
	destinationPath := filepath.Join(destination.Dir, filepath.FromSlash(relative))
	assertFileBytes(t, destinationPath, readFileBytes(t, sourcePath))
}

func assertFileBytes(t *testing.T, path string, expected []byte) {
	t.Helper()
	assert.Equal(t, expected, readFileBytes(t, path), "bytes differ for %s", path)
}

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled fixture or temporary path
	require.NoError(t, err)
	return data
}

type threadRowMetadata struct {
	Title        string
	ArchivedAt   int64
	GitSHA       string
	GitBranch    string
	GitOriginURL string
}

func insertThreadRow(t *testing.T, path, id string, metadata threadRowMetadata) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	_, err = database.ExecContext(t.Context(), `INSERT INTO threads
		(id, rollout_path, created_at, updated_at, source, model_provider, cwd, title, sandbox_policy, approval_mode,
		 archived_at, git_sha, git_branch, git_origin_url)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "archived_sessions/rollout-"+id+".jsonl", 1, 1, "cli", "openai", FixtureProjectPath(), metadata.Title,
		"workspace-write", "on-request", nullableInt64Argument(metadata.ArchivedAt), nullableStringArgument(metadata.GitSHA),
		nullableStringArgument(metadata.GitBranch), nullableStringArgument(metadata.GitOriginURL),
	)
	require.NoError(t, err)
}

func nullableInt64Argument(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableStringArgument(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func assertThreadMetadata(t *testing.T, path, id string, expected threadRowMetadata) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	var title string
	var archivedAt int64
	var sha, branch, origin string
	err = database.QueryRowContext(t.Context(), `SELECT title, archived_at, git_sha, git_branch, git_origin_url
		FROM threads WHERE id = ?`, id).Scan(&title, &archivedAt, &sha, &branch, &origin)
	require.NoError(t, err)
	assert.Equal(t, expected.Title, title)
	assert.Equal(t, expected.ArchivedAt, archivedAt)
	assert.Equal(t, expected.GitSHA, sha)
	assert.Equal(t, expected.GitBranch, branch)
	assert.Equal(t, expected.GitOriginURL, origin)
}
