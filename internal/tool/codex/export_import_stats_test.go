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

	workspace := newWorkspace(home, func(string) string { return "" }, nil, nil, nil, DefaultTranscodeCaps())
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

func TestExportReportsMalformedSharedJSONLLines(t *testing.T) {
	tests := []struct {
		name        string
		file        string
		archiveName string
		category    string
		inScope     []byte
		outOfScope  []byte
	}{
		{
			name:        "session index",
			file:        sessionIndexFile,
			archiveName: "codex/session-index/" + sessionIndexFile,
			category:    categorySessions,
			inScope:     []byte(`{"id":"` + fixtureThreadOne + `","thread_name":"first"}`),
			outOfScope:  []byte(`{"id":"unrelated-thread","thread_name":"other"}`),
		},
		{
			name:        "history",
			file:        codexHistoryFile,
			archiveName: "codex/history/" + codexHistoryFile,
			category:    categoryHistory,
			inScope:     []byte(`{"session_id":"` + fixtureThreadOne + `","ts":100,"text":"first"}`),
			outOfScope:  []byte(`{"session_id":"unrelated-thread","ts":300,"text":"other"}`),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			home := SetupFixture(t)
			contents := append([]byte(nil), testCase.inScope...)
			contents = append(contents, '\n')
			contents = append(contents, []byte("malformed JSON\n")...)
			contents = append(contents, testCase.outOfScope...)
			contents = append(contents, '\n')
			require.NoError(t, os.WriteFile(filepath.Join(home.Dir, testCase.file), contents, 0o600))

			workspace := quietTestWorkspace(home)
			var archiveBytes bytes.Buffer
			writer := zip.NewWriter(&archiveBytes)
			sink := archive.NewSink(writer, toolName, nil)
			result, err := workspace.Export(t.Context(), FixtureProjectPath(), map[string]bool{testCase.category: true}, sink)
			require.NoError(t, err)
			require.NoError(t, writer.Close())

			assert.Contains(t, result.Warnings, "1 malformed line(s) in "+testCase.file+" were omitted during export")
			assert.Len(t, result.Warnings, 2, "the valid out-of-scope line is an expected drop, not a warning")
			assert.Equal(t, append(append([]byte(nil), testCase.inScope...), '\n'), readArchiveEntry(t, archiveBytes.Bytes(), testCase.archiveName))
		})
	}
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
	reader, err := archive.OpenReader(bytes.NewReader(archiveBytes.Bytes()), int64(archiveBytes.Len()), archive.DefaultCaps())
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

func TestCodexRoundTripRekeysProjectPathsToImportTarget(t *testing.T) {
	sourceHome := SetupFixture(t)
	history, index := writeRoundTripLineStores(t, sourceHome)
	archiveBytes := exportFixtureArchive(t, sourceHome)
	destinationHome, config := setupImportDestination(t)
	importTarget := "/Users/recipient/relocated-project"

	result := importFixtureArchiveToTarget(t, archiveBytes, destinationHome, importTarget)

	assert.Empty(t, result.Warnings)
	for _, relative := range []string{eraCPath, eraBPath, "archived_sessions/rollout-2026-07-10T09-00-00-00000000-0000-4000-8000-000000000002.jsonl"} {
		data := readFileBytes(t, filepath.Join(destinationHome.Dir, filepath.FromSlash(relative)))
		assert.NotContains(t, string(data), FixtureProjectPath())
		assert.Contains(t, string(data), `"cwd":"`+importTarget+`"`)
		if relative == eraCPath {
			assert.Contains(t, string(data), `"workspace_roots":["`+importTarget+`"]`)
		}
	}
	archiveRollout := readArchiveEntry(
		t, archiveBytes, "codex/sessions/2026/07/17/rollout-2026-07-17T10-00-00-00000000-0000-4000-8000-000000000001.jsonl",
	)
	assert.Contains(t, string(archiveRollout), codexProjectPathKey)
	assertFileBytes(t, filepath.Join(destinationHome.Dir, configTOMLFileName), config)
	assertFileBytes(t, filepath.Join(destinationHome.Dir, codexHistoryFile), history)
	assertFileBytes(t, filepath.Join(destinationHome.Dir, sessionIndexFile), index)
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

func TestAppendLinesToFileSeparatesTornPriorRecord(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		create  bool
		want    string
	}{
		{name: "torn prior append", initial: "partial record", create: true, want: "partial record\nnew record\n"},
		{name: "trailing newline", initial: "existing record\n", create: true, want: "existing record\nnew record\n"},
		{name: "empty file", create: true, want: "new record\n"},
		{name: "absent file", want: "new record\n"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "line-store.jsonl")
			if testCase.create {
				require.NoError(t, os.WriteFile(path, []byte(testCase.initial), 0o600))
			}

			require.NoError(t, appendLinesToFile(path, [][]byte{[]byte("new record")}))

			assert.Equal(t, testCase.want, string(readFileBytes(t, path)))
		})
	}
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

func TestCodexSidecarRejectsStringArchivedAtWithLineAndField(t *testing.T) {
	home := SetupFixture(t)
	workspace := quietTestWorkspace(home)
	sidecar := `{"thread_id":"` + fixtureThreadOne + `","archived_at":"not-an-integer",` +
		`"title":null,"git":{"sha":null,"branch":null,"origin_url":null}}` + "\n"
	workspace.sidecarAppends = [][]byte{[]byte(sidecar)}

	_, err := workspace.applyThreadSidecars()

	require.Error(t, err)
	require.ErrorContains(t, err, "line 1")
	require.ErrorContains(t, err, "archived_at")
}

func TestCodexSidecarExportKeepsNewestStateGenerationForDuplicateThread(t *testing.T) {
	home := SetupFixture(t)
	setThreadTitle(t, filepath.Join(home.SQLiteDir, "state_5.sqlite"), fixtureThreadOne, "older title")
	buildFixtureStateDB(t, filepath.Join(home.SQLiteDir, "state_12.sqlite"))
	setThreadTitle(t, filepath.Join(home.SQLiteDir, "state_12.sqlite"), fixtureThreadOne, "newer title")

	archiveBytes := exportFixtureArchive(t, home)
	sidecar := readArchiveEntry(t, archiveBytes, "codex/threads-sidecar.jsonl")

	assert.Contains(t, string(sidecar), `"title":"newer title"`)
	assert.NotContains(t, string(sidecar), `"title":"older title"`)
	assert.Equal(t, 1, bytes.Count(sidecar, []byte(fixtureThreadOne)))
}

func TestCodexStageRejectsHostileRolloutNamesAndAcceptsRecorderNames(t *testing.T) {
	workspace := quietTestWorkspace(SetupFixture(t))
	for _, name := range []string{
		"sessions/unexpected.txt",
		"sessions/2026/07/17/nested/rollout-2026-07-17T10-00-00-id.jsonl",
		"sessions/202x/07/17/rollout-2026-07-17T10-00-00-id.jsonl",
		"sessions/2026/7/17/rollout-2026-07-17T10-00-00-id.jsonl",
		"sessions/2026/07/1x/rollout-2026-07-17T10-00-00-id.jsonl",
		"archived-sessions/nested/rollout-2026-07-17T10-00-00-id.jsonl",
	} {
		entry := archiveEntryForTest(t, "codex/"+name)
		_, err := workspace.Stage(t.Context(), FixtureProjectPath(), entry, nil)
		var unknown *UnknownArchiveEntryError
		require.ErrorAs(t, err, &unknown, name)
	}
	for _, name := range []string{
		"sessions/2026/07/17/rollout-2026-07-17T10-00-00-00000000-0000-4000-8000-000000000001.jsonl",
		"archived-sessions/rollout-2026-07-17T10-00-00-00000000-0000-4000-8000-000000000001.jsonl",
	} {
		entry := archiveEntryForTest(t, "codex/"+name)
		staged, err := workspace.Stage(t.Context(), FixtureProjectPath(), entry, nil)
		require.NoError(t, err, name)
		require.Len(t, staged, 1, name)
		_ = os.Remove(staged[0].Temp)
	}
}

func TestCodexAuditsRejectUnknownProjectsAndDoNotAttributeSharedHistoryBytes(t *testing.T) {
	home := SetupFixture(t)
	secondProject := "/Users/fixture/second-project"
	insertThreadRowForProject(t, filepath.Join(home.SQLiteDir, stateDBFileName), "second-thread", secondProject, threadRowMetadata{})
	history := []byte(
		`{"session_id":"` + fixtureThreadOne + `","ts":1}` + "\n" +
			`{"session_id":"second-thread","ts":2}` + "\n",
	)
	require.NoError(t, os.WriteFile(filepath.Join(home.Dir, codexHistoryFile), history, 0o600))
	workspace := quietTestWorkspace(home)

	_, err := workspace.ReferenceSurfaces("/Users/fixture/unknown-project")
	require.ErrorIs(t, err, tool.ErrProjectAbsent)
	_, err = workspace.DiskCategories("/Users/fixture/unknown-project")
	require.ErrorIs(t, err, tool.ErrProjectAbsent)
	first, err := workspace.DiskCategories(FixtureProjectPath())
	require.NoError(t, err)
	second, err := workspace.DiskCategories(secondProject)
	require.NoError(t, err)
	assert.Zero(t, sizeCategoryNamed(first, categoryHistory).Bytes)
	assert.Zero(t, sizeCategoryNamed(second, categoryHistory).Bytes)
}

// TestKnowsProjectHonorsConfigOnlyProjects guards knowsProject's third
// association: a config.toml [projects] key alone must count as known even
// when the project has no thread row and no rollout, matching move.go's
// projectKnown. Before this, EnumerateProjects would enumerate such a
// project and then have its own DiskCategories call reject it, failing the
// whole stats run.
func TestKnowsProjectHonorsConfigOnlyProjects(t *testing.T) {
	home := SetupFixture(t)
	workspace := quietTestWorkspace(home)

	tests := []struct {
		name    string
		project string
		want    bool
	}{
		{name: "config key with no threads or rollouts counts as known", project: "/Users/fixture/other-project", want: true},
		{name: "project absent from every source stays unknown", project: "/Users/fixture/unknown-project", want: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			known, err := workspace.knowsProject(testCase.project)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, known)
		})
	}
}

// TestEnumerateProjectsIncludesConfigOnlyProjectWithZeroFootprint is the
// end-to-end regression for the same bug: EnumerateProjects must list a
// config-key-only project with a zero footprint, and DiskCategories and
// ReferenceSurfaces must both succeed with zeros for it, instead of the
// whole stats run failing with tool.ErrProjectAbsent.
func TestEnumerateProjectsIncludesConfigOnlyProjectWithZeroFootprint(t *testing.T) {
	home := SetupFixture(t)
	workspace := quietTestWorkspace(home)
	const configOnlyProject = "/Users/fixture/other-project"

	projects, err := workspace.EnumerateProjects()
	require.NoError(t, err)

	var info tool.ProjectInfo
	found := false
	for _, candidate := range projects {
		if candidate.Label == configOnlyProject {
			info, found = candidate, true
			break
		}
	}
	require.True(t, found, "EnumerateProjects must list the config-key-only project")
	assert.Zero(t, info.Files)
	assert.Zero(t, info.Bytes)

	disk, err := workspace.DiskCategories(configOnlyProject)
	require.NoError(t, err)
	for _, category := range disk {
		assert.Zero(t, category.Files, "category %s", category.Name)
		assert.Zero(t, category.Bytes, "category %s", category.Name)
	}

	references, err := workspace.ReferenceSurfaces(configOnlyProject)
	require.NoError(t, err)
	for _, surface := range references {
		assert.Zero(t, surface.Count, "surface %s", surface.Name)
	}
}

// TestConfigCommentOrValueDoesNotConferProjectKnowledge guards the precision
// of configTOMLKnowsProject: it parses the "projects" table rather than
// scanning raw bytes, so a path occurring only in a comment or in an
// unrelated (non-projects) value must never be mistaken for a [projects]
// table key.
func TestConfigCommentOrValueDoesNotConferProjectKnowledge(t *testing.T) {
	dir := t.TempDir()
	home := &Home{Dir: dir, SQLiteDir: dir}
	const commentOnlyProject = "/Users/fixture/comment-only-project"
	config := "# see " + commentOnlyProject + " for context\n" +
		"note = \"" + commentOnlyProject + "\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, configTOMLFileName), []byte(config), 0o600))
	workspace := quietTestWorkspace(home)

	known, err := workspace.knowsProject(commentOnlyProject)
	require.NoError(t, err)
	assert.False(t, known, "a path in a comment or unrelated value must not confer project knowledge")

	_, err = workspace.DiskCategories(commentOnlyProject)
	require.ErrorIs(t, err, tool.ErrProjectAbsent)
	_, err = workspace.ReferenceSurfaces(commentOnlyProject)
	require.ErrorIs(t, err, tool.ErrProjectAbsent)
}

// TestEnumerateProjectsIncludesProfileOnlyProject guards the second
// precision gap: EnumerateProjects must read every *.config.toml profile
// overlay, not only the top-level config.toml, so a project trusted solely
// through a profile is still enumerated with a zero footprint.
func TestEnumerateProjectsIncludesProfileOnlyProject(t *testing.T) {
	dir := t.TempDir()
	home := &Home{Dir: dir, SQLiteDir: dir}
	const profileOnlyProject = "/Users/fixture/profile-only-project"
	profileConfig := "[projects.\"" + profileOnlyProject + "\"]\ntrust_level = \"trusted\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "work.config.toml"), []byte(profileConfig), 0o600))
	workspace := quietTestWorkspace(home)

	projects, err := workspace.EnumerateProjects()
	require.NoError(t, err)

	var info tool.ProjectInfo
	found := false
	for _, candidate := range projects {
		if candidate.Label == profileOnlyProject {
			info, found = candidate, true
			break
		}
	}
	require.True(t, found, "EnumerateProjects must include a project known only via a *.config.toml profile overlay")
	assert.Zero(t, info.Files)
	assert.Zero(t, info.Bytes)
}

func TestCodexExportsThreadOnlyProjectState(t *testing.T) {
	home := SetupFixture(t)
	require.NoError(t, os.RemoveAll(filepath.Join(home.Dir, sessionsSubdir)))
	require.NoError(t, os.RemoveAll(filepath.Join(home.Dir, archivedSessionsSubdir)))
	history, _ := writeRoundTripLineStores(t, home)
	workspace := quietTestWorkspace(home)
	var archiveBytes bytes.Buffer
	writer := zip.NewWriter(&archiveBytes)
	sink := archive.NewSink(writer, toolName, nil)

	_, err := workspace.Export(t.Context(), FixtureProjectPath(), map[string]bool{categorySessions: true, categoryHistory: true}, sink)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	assert.Equal(t, bytes.SplitAfter(history, []byte("\n"))[0], readArchiveEntry(t, archiveBytes.Bytes(), "codex/history/"+codexHistoryFile))
	assert.NotEmpty(t, readArchiveEntry(t, archiveBytes.Bytes(), "codex/threads-sidecar.jsonl"))
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
	return importFixtureArchiveToTarget(t, data, home, FixtureProjectPath())
}

func importFixtureArchiveToTarget(t *testing.T, data []byte, home *Home, target string) *importer.Result {
	t.Helper()
	adapter := New()
	set := tool.NewSet(adapter)
	reader := bytes.NewReader(data)
	result, err := importer.Run(t.Context(), set, []tool.Target{{Tool: adapter, Workspace: quietTestWorkspace(home)}}, &importer.Options{
		Source: reader, Size: int64(reader.Len()), TargetPath: target, Caps: archive.DefaultCaps(),
	})
	require.NoError(t, err)
	return result
}

func archiveEntryForTest(t *testing.T, name string) archive.Entry {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	entryWriter, err := writer.Create(name)
	require.NoError(t, err)
	_, err = entryWriter.Write([]byte("{}\n"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	reader, err := archive.OpenReader(bytes.NewReader(data.Bytes()), int64(data.Len()), archive.DefaultCaps())
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	return entries[0].Entry
}

func readArchiveEntry(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		body, openErr := file.Open()
		require.NoError(t, openErr)
		contents, readErr := io.ReadAll(body)
		require.NoError(t, readErr)
		require.NoError(t, body.Close())
		return contents
	}
	t.Fatalf("archive entry %s was not found", name)
	return nil
}

func quietTestWorkspace(home *Home) *Workspace {
	return newWorkspace(
		home, func(string) string { return "" }, func() ([]ProcessInfo, error) { return nil, nil },
		func() time.Time { return time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC) }, func(int) bool { return false }, DefaultTranscodeCaps(),
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
	insertThreadRowForProject(t, path, id, FixtureProjectPath(), metadata)
}

func insertThreadRowForProject(t *testing.T, path, id, project string, metadata threadRowMetadata) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	_, err = database.ExecContext(t.Context(), `INSERT INTO threads
		(id, rollout_path, created_at, updated_at, source, model_provider, cwd, title, sandbox_policy, approval_mode,
		 archived_at, git_sha, git_branch, git_origin_url)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "archived_sessions/rollout-"+id+".jsonl", 1, 1, "cli", "openai", project, metadata.Title,
		"workspace-write", "on-request", nullableInt64Argument(metadata.ArchivedAt), nullableStringArgument(metadata.GitSHA),
		nullableStringArgument(metadata.GitBranch), nullableStringArgument(metadata.GitOriginURL),
	)
	require.NoError(t, err)
}

func setThreadTitle(t *testing.T, path, id, title string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	_, err = database.ExecContext(t.Context(), `UPDATE threads SET title = ? WHERE id = ?`, title, id)
	require.NoError(t, err)
}

func sizeCategoryNamed(categories []tool.SizeCategory, name string) tool.SizeCategory {
	for _, category := range categories {
		if category.Name == name {
			return category
		}
	}
	return tool.SizeCategory{Name: name}
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
