package export_test

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	outputPath := filepath.Join(t.TempDir(), "export.zip")

	result, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   outputPath,
		Categories:   manifest.CategorySet{Sessions: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

	assert.NotEmpty(t, result.Sessions, "at least one sessions entry must be present")
}

func TestExport_IncludesMemory(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")

	result, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   outputPath,
		Categories:   manifest.CategorySet{Memory: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

	assert.NotEmpty(t, result.Memory, "at least one memory entry must be present")
}

func TestExport_IncludesHistory(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")

	result, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   outputPath,
		Categories:   manifest.CategorySet{History: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

	require.Len(t, result.History, 1, "history must produce exactly one entry")
	contents := readZipContents(t, outputPath)
	assert.NotEmpty(t, contents[result.History[0].ArchivePath], "history body must not be empty")
}

func TestExport_IncludesConfig(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")

	result, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   outputPath,
		Categories:   manifest.CategorySet{Config: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

	assert.Len(t, result.Config, 1, "config must produce exactly one entry")
}

func TestExport_RedactsProjectPaths(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")

	_, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories: manifest.CategorySet{
			Sessions: true, Memory: true, History: true, Config: true,
		},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

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

	_, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories: manifest.CategorySet{
			Sessions: true, Memory: true, History: true, Config: true,
		},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

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
	outputPath := filepath.Join(t.TempDir(), "export.zip")

	options := export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories: manifest.CategorySet{
			Sessions:    false,
			Memory:      true,
			History:     false,
			FileHistory: false,
			Config:      false,
		},
		Placeholders: defaultPlaceholders(),
	}

	result, err := export.Run(t.Context(), claudeHome, options)
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
	_, err := export.Run(t.Context(), claudeHome1, export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   out1,
		Categories:   manifest.CategorySet{Sessions: true, Memory: true, History: true, Config: true},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

	claudeHome2 := testutil.SetupFixture(t)
	out2 := filepath.Join(t.TempDir(), "export-shorter-first.zip")
	reversed := []manifest.Placeholder{
		{Key: "{{HOME}}", Original: "/Users/test"},
		{Key: "{{PROJECT_PATH}}", Original: fixtureProjectPath},
	}
	_, err = export.Run(t.Context(), claudeHome2, export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   out2,
		Categories:   manifest.CategorySet{Sessions: true, Memory: true, History: true, Config: true},
		Placeholders: reversed,
	})
	require.NoError(t, err)

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
	require.NoError(t, os.WriteFile(claudeHome.HistoryFile(), historyData, 0600))

	outputPath := filepath.Join(t.TempDir(), "export.zip")
	_, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories:  manifest.CategorySet{History: true},
	})
	require.NoError(t, err)

	contents := readZipContents(t, outputPath)
	return contents["history/history.jsonl"]
}

func TestExport_IncludesTodos(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "out.zip")

	result, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath: "/Users/test/Projects/myproject",
		OutputPath:  archivePath,
		Categories:  manifest.CategorySet{Todos: true},
	})
	require.NoError(t, err)

	assert.NotEmpty(t, result.Todos, "archive must contain at least one todos entry")
}

func TestExport_IncludesUsageData(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "out.zip")

	result, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath: "/Users/test/Projects/myproject",
		OutputPath:  archivePath,
		Categories:  manifest.CategorySet{UsageData: true},
	})
	require.NoError(t, err)

	assert.NotEmpty(t, result.UsageData, "archive must contain usage-data entries")
}

func TestExport_IncludesPluginsData(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "out.zip")

	result, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath: "/Users/test/Projects/myproject",
		OutputPath:  archivePath,
		Categories:  manifest.CategorySet{PluginsData: true},
	})
	require.NoError(t, err)

	require.NotEmpty(t, result.PluginsData)
	assert.Contains(t, result.PluginsData[0].ArchivePath, "example-plugin/",
		"plugin namespace must appear in the archive path")
}

func TestExport_IncludesTasks_SkipsSidecars(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "out.zip")

	result, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath: "/Users/test/Projects/myproject",
		OutputPath:  archivePath,
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

	_, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath: "/Users/test/Projects/myproject",
		OutputPath:  archivePath,
		Categories:  manifest.CategorySet{Sessions: true},
	})
	require.NoError(t, err)

	metadata, err := manifest.ReadManifestFromZip(archivePath)
	require.NoError(t, err)

	expected := []string{"sessions", "memory", "history", "file-history", "config",
		"todos", "usage-data", "plugins-data", "tasks"}
	var got []string
	for _, c := range metadata.Export.Categories {
		got = append(got, c.Name)
	}
	assert.ElementsMatch(t, expected, got, "every export must declare all 9 category names")
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

	_, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories: manifest.CategorySet{
			Memory: true, Sessions: true, History: true, Config: true,
		},
		Placeholders: defaultPlaceholders(),
	})
	require.NoError(t, err)

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
	outputPath := filepath.Join(t.TempDir(), "out.zip")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := export.Run(ctx, claudeHome, export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories:  manifest.CategorySet{Sessions: true, History: true},
	})

	require.ErrorIs(t, err, context.Canceled)
	require.NoFileExists(t, outputPath)
}

// TestRun_SessionTranscriptsMatchAppliedPlaceholders is a byte-identity
// regression guard for the streaming ZIP writer refactor. Each session
// transcript archive entry must equal applyPlaceholders(os.ReadFile(source))
// run against the on-disk fixture, line by line — ensuring the streaming
// path produces the same bytes as the pre-streaming whole-file path.
func TestRun_SessionTranscriptsMatchAppliedPlaceholders(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "out.zip")
	placeholders := defaultPlaceholders()

	_, err := export.Run(t.Context(), claudeHome, export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   outputPath,
		Categories:   manifest.CategorySet{Sessions: true},
		Placeholders: placeholders,
	})
	require.NoError(t, err)

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
