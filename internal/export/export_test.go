package export_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/testutil"
)

const fixtureProjectPath = "/Users/test/Projects/myproject"

func defaultPlaceholders() []manifest.Placeholder {
	return []manifest.Placeholder{
		{Key: "{{PROJECT_PATH}}", Original: fixtureProjectPath},
		{Key: "{{HOME}}", Original: "/Users/test"},
	}
}

// createArchiveFile opens path with O_CREATE|O_TRUNC and registers a
// Cleanup that closes it. Returns the file so callers can pass it as
// export.Options.Output and re-open path for assertion-time reads after
// export.Run finalizes the zip.
func createArchiveFile(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.Create(path) //nolint:gosec // G304: test-controlled tempdir path
	require.NoError(t, err, "create archive file %s", path)
	t.Cleanup(func() { _ = file.Close() })
	return file
}

// readZipContents opens the ZIP at zipPath and returns a map of filename → content.
func readZipContents(t *testing.T, zipPath string) map[string]string {
	t.Helper()

	reader, err := zip.OpenReader(zipPath)
	require.NoError(t, err, "open zip archive")
	defer func() { _ = reader.Close() }()

	contents := make(map[string]string, len(reader.File))
	for _, file := range reader.File {
		rc, err := file.Open()
		require.NoError(t, err, "open zip entry %s", file.Name)
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		require.NoError(t, err, "read zip entry %s", file.Name)
		contents[file.Name] = string(data)
	}
	return contents
}

func TestExport_IncludesSessions(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       &buf,
		Categories:   manifest.CategorySet{Sessions: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

	assert.NotEmpty(t, result.Sessions, "at least one sessions entry must be present")
}

func TestExport_IncludesMemory(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       &buf,
		Categories:   manifest.CategorySet{Memory: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

	assert.NotEmpty(t, result.Memory, "at least one memory entry must be present")
}

func TestExport_IncludesHistory(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")
	output := createArchiveFile(t, outputPath)

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       output,
		Categories:   manifest.CategorySet{History: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)
	require.NoError(t, output.Close())

	require.Len(t, result.History, 1, "history must produce exactly one entry")
	contents := readZipContents(t, outputPath)
	assert.NotEmpty(t, contents[result.History[0].ArchivePath], "history body must not be empty")
}

func TestExport_IncludesConfig(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       &buf,
		Categories:   manifest.CategorySet{Config: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

	assert.Len(t, result.Config, 1, "config must produce exactly one entry")
}

func TestExport_IncludesFileHistory(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       &buf,
		Categories:   manifest.CategorySet{FileHistory: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

	assert.NotEmpty(t, result.FileHistory, "at least one file-history entry must be present")
}

func TestExport_FileHistorySkippedWhenDisabled(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       &buf,
		Categories:   manifest.CategorySet{Sessions: true, FileHistory: false},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

	assert.Empty(t, result.FileHistory, "file-history must be empty when category is off")
	assert.NotEmpty(t, result.Sessions, "sessions must still be populated as the carrier category")
}

func TestExport_FileHistoryEntriesUseUUIDPrefix(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       &buf,
		Categories:   manifest.CategorySet{FileHistory: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.FileHistory)

	for _, entry := range result.FileHistory {
		assert.True(t, strings.HasPrefix(entry.ArchivePath, "file-history/"),
			"entry %q must start with file-history/", entry.ArchivePath)
		// After "file-history/", the next path component is a uuid, then a /
		// before the snapshot file name. Smallest valid shape: 13 + 36 + 1 + 1 = 51 chars.
		assert.GreaterOrEqual(t, len(entry.ArchivePath), len("file-history/")+36+1+1,
			"entry %q must include a uuid segment and a snapshot name", entry.ArchivePath)
	}
}

func TestExport_FileHistoryArchivesAllSnapshots(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	expectedCount := fileHistoryFixtureSnapshotCount(t, claudeHome)
	require.Positive(t, expectedCount, "fixture must contain at least one project-relevant snapshot")

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       &buf,
		Categories:   manifest.CategorySet{FileHistory: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

	assert.Len(t, result.FileHistory, expectedCount,
		"every fixture snapshot under project-relevant uuids must produce one archive entry")
}

func TestExport_FileHistoryBytesAreVerbatim(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")
	output := createArchiveFile(t, outputPath)

	// Pick the lex-first project-relevant uuid dir, then the lex-first
	// snapshot inside it. This mirrors the deterministic-pick pattern used
	// by chmod-based tests and stays stable under fixture additions.
	locations, err := claude.LocateProject(claudeHome, fixtureProjectPath)
	require.NoError(t, err)
	require.NotEmpty(t, locations.FileHistoryDirs)
	sort.Strings(locations.FileHistoryDirs)
	firstDir := locations.FileHistoryDirs[0]

	dirEntries, err := os.ReadDir(firstDir)
	require.NoError(t, err)
	require.NotEmpty(t, dirEntries)
	sort.Slice(dirEntries, func(i, j int) bool { return dirEntries[i].Name() < dirEntries[j].Name() })
	snapshotName := dirEntries[0].Name()
	snapshotPath := filepath.Join(firstDir, snapshotName)

	// Body that would be redacted by applyPlaceholders if the file-history
	// pipeline ever started running it. Includes both placeholders' Original
	// values to make accidental redaction visible from either direction.
	verbatimBody := []byte("see /Users/test/Projects/myproject/main.go and /Users/test/notes\n")
	require.NoError(t, os.WriteFile(snapshotPath, verbatimBody, 0o600))

	_, err = export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       output,
		Categories:   manifest.CategorySet{FileHistory: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)
	require.NoError(t, output.Close())

	uuid := filepath.Base(firstDir)
	zipName := "file-history/" + uuid + "/" + snapshotName
	contents := readZipContents(t, outputPath)
	require.Contains(t, contents, zipName, "archive must contain the mutated snapshot entry")
	assert.Equal(t, string(verbatimBody), contents[zipName],
		"file-history bytes must be archived verbatim (no placeholder substitution)")
}

func TestExport_RedactsProjectPaths(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")
	output := createArchiveFile(t, outputPath)

	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: fixtureProjectPath,
		Output:      output,
		Categories: manifest.CategorySet{
			Sessions: true, Memory: true, History: true, Config: true,
		},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)
	require.NoError(t, output.Close())

	contents := readZipContents(t, outputPath)
	for name, content := range contents {
		if name == "metadata.xml" {
			continue
		}
		assert.NotContains(t, content, fixtureProjectPath,
			"file %s must not contain original project path", name)
		assert.NotContains(t, content, "/Users/test",
			"file %s must not contain original home path", name)
	}
}

func TestExport_AddsPlaceholderForProjectPath(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")
	output := createArchiveFile(t, outputPath)

	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: fixtureProjectPath,
		Output:      output,
		Categories: manifest.CategorySet{
			Sessions: true, Memory: true, History: true, Config: true,
		},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)
	require.NoError(t, output.Close())

	contents := readZipContents(t, outputPath)
	found := false
	for name, content := range contents {
		if strings.HasPrefix(name, "sessions/") && strings.Contains(content, "{{PROJECT_PATH}}") {
			found = true
			break
		}
	}
	assert.True(t, found,
		"at least one anonymized session file must contain {{PROJECT_PATH}}")
}

func TestExport_SelectiveCategories(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	options := export.Options{
		ProjectPath: fixtureProjectPath,
		Output:      &buf,
		Categories: manifest.CategorySet{
			Sessions:    false,
			Memory:      true,
			History:     false,
			FileHistory: false,
			Config:      false,
		},
		Placeholders: defaultPlaceholders(),
	}

	result, err := export.Run(t.Context(), claudeHome, &options)
	require.NoError(t, err)

	assert.NotEmpty(t, result.Memory, "memory must be present when enabled")
	assert.Empty(t, result.Sessions, "sessions must be absent when disabled")
	assert.Empty(t, result.History, "history must be absent when disabled")
	assert.Empty(t, result.Config, "config must be absent when disabled")
	assert.Equal(t, "metadata.xml", result.Metadata.ArchivePath)
}

func TestExport_PathAnonymization_OrderIndependent(t *testing.T) {
	// The anonymizer sorts placeholders by Original length descending so
	// nested paths (e.g. {{HOME}}=/Users/test is a prefix of
	// {{PROJECT_PATH}}=/Users/test/Projects/myproject) always resolve with
	// the most specific match winning. Caller-declared order must therefore
	// not affect the output — swap the Placeholder slice order and verify
	// byte-for-byte equality.
	claudeHome1 := testutil.SetupFixture(t)
	out1 := filepath.Join(t.TempDir(), "export-longer-first.zip")
	output1 := createArchiveFile(t, out1)
	_, err := export.Run(t.Context(), claudeHome1, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       output1,
		Categories:   manifest.CategorySet{Sessions: true, Memory: true, History: true, Config: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)
	require.NoError(t, output1.Close())

	claudeHome2 := testutil.SetupFixture(t)
	out2 := filepath.Join(t.TempDir(), "export-shorter-first.zip")
	output2 := createArchiveFile(t, out2)
	reversed := []manifest.Placeholder{
		{Key: "{{HOME}}", Original: "/Users/test"},
		{Key: "{{PROJECT_PATH}}", Original: fixtureProjectPath},
	}
	_, err = export.Run(t.Context(), claudeHome2, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       output2,
		Categories:   manifest.CategorySet{Sessions: true, Memory: true, History: true, Config: true},
		Placeholders: reversed,
	})
	require.NoError(t, err)
	require.NoError(t, output2.Close())

	// Every non-metadata entry must be byte-identical between the two orderings.
	// metadata.xml is excluded because it encodes a `created` timestamp.
	contents1 := readZipContents(t, out1)
	contents2 := readZipContents(t, out2)
	for name, content := range contents1 {
		if name == "metadata.xml" {
			continue
		}
		assert.Equal(t, content, contents2[name],
			"entry %s must be identical across placeholder orderings", name)
	}
}

func TestExport_HistoryIncludesStructuredProjectMatch(t *testing.T) {
	body := historyExportWithLines(t, []string{
		`{"marker":"structured-match","project":"/Users/test/Projects/myproject","display":"a"}`,
	})
	assert.Contains(t, body, `"marker":"structured-match"`,
		"structured project-field match must be included (branch 1)")
}

func TestExport_HistoryIncludesEmptyProjectWithBoundedReference(t *testing.T) {
	body := historyExportWithLines(t, []string{
		`{"marker":"empty-with-ref","project":"","display":"open /Users/test/Projects/myproject/main.go"}`,
	})
	assert.Contains(t, body, `"marker":"empty-with-ref"`,
		"empty project + bounded reference must be included (branch 2)")
}

func TestExport_HistoryIncludesMalformedWithBoundedReference(t *testing.T) {
	body := historyExportWithLines(t, []string{
		`{malformed "marker":"malformed-with-ref" /Users/test/Projects/myproject/foo.go`,
	})
	assert.Contains(t, body, `"marker":"malformed-with-ref"`,
		"malformed line with bounded reference must be included (branch 3)")
}

func TestExport_HistoryExcludesOtherProjectWithReference(t *testing.T) {
	body := historyExportWithLines(t, []string{
		`{"marker":"other","project":"/Users/test/Projects/otherproject",` +
			`"display":"inspired by /Users/test/Projects/myproject"}`,
	})
	assert.NotContains(t, body, `"marker":"other"`,
		"line tagged to a different project must NOT be included even if it mentions our path")
}

func TestExport_HistoryExcludesPrefixCollisionSibling(t *testing.T) {
	body := historyExportWithLines(t, []string{
		`{"marker":"prefix-collision","project":"",` +
			`"display":"notes live in /Users/test/Projects/myproject-extras/"}`,
	})
	assert.NotContains(t, body, `"marker":"prefix-collision"`,
		"prefix-collision sibling path (myproject-extras) must NOT register as a match")
}

func TestExport_HistoryExcludesMalformedWithoutReference(t *testing.T) {
	body := historyExportWithLines(t, []string{
		`{malformed "marker":"malformed-no-ref" unrelated content`,
	})
	assert.NotContains(t, body, `"marker":"malformed-no-ref"`,
		"malformed line without any reference to our path must NOT be included")
}

func TestExport_HistoryExcludesUnrelatedStructured(t *testing.T) {
	body := historyExportWithLines(t, []string{
		`{"marker":"unrelated","project":"/Users/test/Projects/otherproject","display":"z"}`,
	})
	assert.NotContains(t, body, `"marker":"unrelated"`,
		"line tagged to a different project with no reference must NOT be included")
}

// historyExportWithLines overwrites the fixture's history.jsonl with the
// given lines, runs an export with only History enabled, and returns the
// exported history body. May be empty when the input lines all fall under
// an exclusion rule.
func historyExportWithLines(t *testing.T, lines []string) string {
	t.Helper()
	claudeHome := testutil.SetupFixture(t)
	historyData := []byte(strings.Join(lines, "\n") + "\n")
	require.NoError(t, os.WriteFile(claudeHome.HistoryFile(), historyData, 0o600))

	outputPath := filepath.Join(t.TempDir(), "export.zip")
	output := createArchiveFile(t, outputPath)
	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: fixtureProjectPath,
		Output:      output,
		Categories:  manifest.CategorySet{History: true},
	})
	require.NoError(t, err)
	require.NoError(t, output.Close())

	contents := readZipContents(t, outputPath)
	return contents["history/history.jsonl"]
}

func TestExport_IncludesTodos(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: "/Users/test/Projects/myproject",
		Output:      &buf,
		Categories:  manifest.CategorySet{Todos: true},
	})
	require.NoError(t, err)

	assert.NotEmpty(t, result.Todos, "archive must contain at least one todos entry")
}

func TestExport_IncludesUsageData(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: "/Users/test/Projects/myproject",
		Output:      &buf,
		Categories:  manifest.CategorySet{UsageData: true},
	})
	require.NoError(t, err)

	assert.NotEmpty(t, result.UsageData, "archive must contain usage-data entries")
}

func TestExport_IncludesPluginsData(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: "/Users/test/Projects/myproject",
		Output:      &buf,
		Categories:  manifest.CategorySet{PluginsData: true},
	})
	require.NoError(t, err)

	require.NotEmpty(t, result.PluginsData)
	assert.Contains(t, result.PluginsData[0].ArchivePath, "example-plugin/",
		"plugin namespace must appear in the archive path")
}

func TestExport_IncludesTasks_SkipsSidecars(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	result, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: "/Users/test/Projects/myproject",
		Output:      &buf,
		Categories:  manifest.CategorySet{Tasks: true},
	})
	require.NoError(t, err)

	require.NotEmpty(t, result.Tasks, "task JSON file must be in the archive")
	for _, entry := range result.Tasks {
		assert.NotEqual(t, ".lock", filepath.Base(entry.ArchivePath),
			".lock sidecar must be excluded")
		assert.NotEqual(t, ".highwatermark", filepath.Base(entry.ArchivePath),
			".highwatermark sidecar must be excluded")
	}
}

func TestExport_ManifestDeclaresAllNineCategories(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "out.zip")
	output := createArchiveFile(t, archivePath)

	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: "/Users/test/Projects/myproject",
		Output:      output,
		Categories:  manifest.CategorySet{Sessions: true},
	})
	require.NoError(t, err)
	require.NoError(t, output.Close())

	zipFile, err := os.Open(archivePath) //nolint:gosec // G304: test-controlled temp path
	require.NoError(t, err, "open archive")
	t.Cleanup(func() { _ = zipFile.Close() })
	zipInfo, err := zipFile.Stat()
	require.NoError(t, err, "stat archive")
	metadata, err := manifest.ReadManifestFromZip(zipFile, zipInfo.Size())
	require.NoError(t, err)

	expected := []string{"sessions", "memory", "history", "file-history", "config",
		"todos", "usage-data", "plugins-data", "tasks"}
	var got []string
	for _, c := range metadata.Export.Categories {
		got = append(got, c.Name)
	}
	assert.ElementsMatch(t, expected, got, "every export must declare all 9 category names")
}

func TestExport_IncludesSyncFieldsWhenSet(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "out.zip")
	output := createArchiveFile(t, archivePath)
	pushedAt := time.Date(2026, 4, 25, 14, 32, 18, 0, time.UTC)

	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       output,
		Categories:   manifest.CategorySet{Sessions: true},
		Placeholders: defaultPlaceholders(),
		SyncPushedBy: "laptop1-alice",
		SyncPushedAt: pushedAt,
	})
	require.NoError(t, err)
	require.NoError(t, output.Close())

	zipFile, err := os.Open(archivePath) //nolint:gosec // G304: test-controlled temp path
	require.NoError(t, err)
	t.Cleanup(func() { _ = zipFile.Close() })
	zipInfo, err := zipFile.Stat()
	require.NoError(t, err)
	metadata, err := manifest.ReadManifestFromZip(zipFile, zipInfo.Size())
	require.NoError(t, err)

	assert.Equal(t, "laptop1-alice", metadata.SyncPushedBy)
	assert.Equal(t, "2026-04-25T14:32:18Z", metadata.SyncPushedAt)
}

func TestExport_OmitsSyncFieldsWhenZero(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "out.zip")
	output := createArchiveFile(t, archivePath)

	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       output,
		Categories:   manifest.CategorySet{Sessions: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)
	require.NoError(t, output.Close())

	contents := readZipContents(t, archivePath)
	metadataXML, ok := contents["metadata.xml"]
	require.True(t, ok, "archive must contain metadata.xml")
	assert.NotContains(t, metadataXML, "<sync-pushed-by",
		"sync-pushed-by element must be absent when SyncPushedBy is zero")
	assert.NotContains(t, metadataXML, "<sync-pushed-at",
		"sync-pushed-at element must be absent when SyncPushedAt is zero")
}

func TestExport_PathAnonymization_BoundaryCollision(t *testing.T) {
	// The fixture memory file contains a reference to
	// `/Users/test/Projects/myproject-extras`, a sibling project whose path
	// is a path-continuation-collision with {{PROJECT_PATH}}. Boundary-aware
	// anonymise must NOT produce `{{PROJECT_PATH}}-extras` (the bug the old
	// strings.ReplaceAll had). The HOME prefix may still be anonymised; what
	// matters is that the `-extras` suffix survives and is not glued onto
	// the PROJECT_PATH token.
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")
	output := createArchiveFile(t, outputPath)

	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath: fixtureProjectPath,
		Output:      output,
		Categories: manifest.CategorySet{
			Memory: true, Sessions: true, History: true, Config: true,
		},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)
	require.NoError(t, output.Close())

	contents := readZipContents(t, outputPath)
	memory := contents["memory/project_notes.md"]
	require.NotEmpty(t, memory, "memory/project_notes.md must be present")

	assert.Contains(t, memory, "{{PROJECT_PATH}}",
		"standalone project path must be anonymized")
	assert.NotContains(t, memory, "{{PROJECT_PATH}}-extras",
		"boundary-aware anonymizer must not produce {{PROJECT_PATH}}-extras")
	// The sibling's myproject-extras suffix must survive verbatim — only
	// the HOME segment ahead of it may be rewritten.
	assert.Contains(t, memory, "Projects/myproject-extras",
		"-extras suffix must not be lost to a broken substitution")
}

func TestRun_CancelsWhenContextCancelled(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	var buf bytes.Buffer

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := export.Run(ctx, claudeHome, &export.Options{
		ProjectPath: fixtureProjectPath,
		Output:      &buf,
		Categories:  manifest.CategorySet{Sessions: true, History: true},
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, buf.Len(),
		"cancel-before-start must not write any bytes to the output writer")
}

// TestRun_SessionTranscriptsMatchAppliedPlaceholders is a byte-identity
// regression guard for the streaming ZIP writer refactor. Each session
// transcript archive entry must equal applyPlaceholders(os.ReadFile(source))
// run against the on-disk fixture, line by line — ensuring the streaming
// path produces the same bytes as the pre-streaming whole-file path.
func TestRun_SessionTranscriptsMatchAppliedPlaceholders(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "out.zip")
	output := createArchiveFile(t, outputPath)
	placeholders := defaultPlaceholders()

	_, err := export.Run(t.Context(), claudeHome, &export.Options{
		ProjectPath:  fixtureProjectPath,
		Output:       output,
		Categories:   manifest.CategorySet{Sessions: true},
		Placeholders: placeholders,
	})
	require.NoError(t, err)
	require.NoError(t, output.Close())

	encodedProjectDir := claudeHome.ProjectDir(fixtureProjectPath)
	contents := readZipContents(t, outputPath)
	sessionEntries := sessionEntryNames(t, contents)
	require.NotEmpty(t, sessionEntries, "fixture must produce at least one sessions/ entry")

	for _, zipName := range sessionEntries {
		relative := strings.TrimPrefix(zipName, "sessions/")
		sourcePath := filepath.Join(encodedProjectDir, relative)
		sourceBytes, readErr := os.ReadFile(sourcePath) //nolint:gosec // G304: fixture path under t.TempDir()
		require.NoError(t, readErr, "read source transcript %s", sourcePath)

		reference := export.ApplyPlaceholders(sourceBytes, placeholders)
		assert.Equal(t, string(reference), contents[zipName],
			"streaming ZIP entry %s must match applyPlaceholders(os.ReadFile(source))", zipName)
	}
}

// sessionEntryNames returns every archive entry whose name starts with
// "sessions/". Deterministic ordering via a lexicographic sort so failing
// assertions point at a stable name.
func sessionEntryNames(t *testing.T, contents map[string]string) []string {
	t.Helper()
	var names []string
	for name := range contents {
		if strings.HasPrefix(name, "sessions/") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// fileHistoryFixtureSnapshotCount returns the count of regular files under
// every file-history dir LocateProject would yield for the standard fixture
// project. Mirrors the iteration exportFileHistory itself performs, so the
// count is definitionally correct under any future fixture rename.
func fileHistoryFixtureSnapshotCount(t *testing.T, claudeHome *claude.Home) int {
	t.Helper()
	locations, err := claude.LocateProject(claudeHome, fixtureProjectPath)
	require.NoError(t, err, "locate project")

	count := 0
	for _, dir := range locations.FileHistoryDirs {
		walkErr := filepath.WalkDir(dir, func(_ string, entry os.DirEntry, perr error) error {
			if perr != nil {
				return perr
			}
			if !entry.IsDir() {
				count++
			}
			return nil
		})
		require.NoError(t, walkErr, "walk file-history dir %s", dir)
	}
	return count
}
