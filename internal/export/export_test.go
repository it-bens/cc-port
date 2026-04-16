package export_test

import (
	"archive/zip"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/testutil"
)

const fixtureProjectPath = "/Users/test/Projects/myproject"

func defaultPlaceholders() []export.Placeholder {
	return []export.Placeholder{
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

func TestExport_AllCategories(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")

	options := export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories: export.CategorySet{
			Sessions:    true,
			Memory:      true,
			History:     true,
			FileHistory: true,
			Config:      true,
		},
		Placeholders: defaultPlaceholders(),
	}

	err := export.Run(claudeHome, options)
	require.NoError(t, err)

	contents := readZipContents(t, outputPath)

	assert.Contains(t, contents, "metadata.xml", "metadata.xml must be present")

	// At least one session transcript should be present under sessions/
	hasSession := false
	for name := range contents {
		if strings.HasPrefix(name, "sessions/") && strings.HasSuffix(name, ".jsonl") {
			hasSession = true
			break
		}
	}
	assert.True(t, hasSession, "at least one sessions/*.jsonl entry must be present")

	// At least one memory file should be present
	hasMemory := false
	for name := range contents {
		if strings.HasPrefix(name, "memory/") {
			hasMemory = true
			break
		}
	}
	assert.True(t, hasMemory, "at least one memory/* entry must be present")

	assert.Contains(t, contents, "history/history.jsonl", "history/history.jsonl must be present")
	assert.Contains(t, contents, "config.json", "config.json must be present")

	// Verify history contains entries for our project
	historyContent := contents["history/history.jsonl"]
	assert.NotEmpty(t, historyContent, "history/history.jsonl must not be empty")
}

func TestExport_PathAnonymization(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")

	options := export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories: export.CategorySet{
			Sessions:    true,
			Memory:      true,
			History:     true,
			FileHistory: false,
			Config:      true,
		},
		Placeholders: defaultPlaceholders(),
	}

	err := export.Run(claudeHome, options)
	require.NoError(t, err)

	contents := readZipContents(t, outputPath)

	originalProjectPath := fixtureProjectPath
	originalHomePath := "/Users/test"

	for name, content := range contents {
		if name == "metadata.xml" {
			// metadata.xml intentionally contains original paths in placeholder attributes
			continue
		}

		assert.NotContains(t, content, originalProjectPath,
			"file %s must not contain original project path", name)
		assert.NotContains(t, content, originalHomePath,
			"file %s must not contain original home path", name)
	}

	// Verify placeholders appear in the anonymized files. Transcripts always
	// carry the project path in their `cwd` field, so at least one
	// sessions/*.jsonl entry must contain {{PROJECT_PATH}} after anonymization.
	foundProjectPlaceholder := false
	for name, content := range contents {
		if strings.HasPrefix(name, "sessions/") && strings.Contains(content, "{{PROJECT_PATH}}") {
			foundProjectPlaceholder = true
			break
		}
	}
	assert.True(t, foundProjectPlaceholder,
		"at least one anonymized session file must contain the project path placeholder")
}

func TestExport_SelectiveCategories(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)
	outputPath := filepath.Join(t.TempDir(), "export.zip")

	options := export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories: export.CategorySet{
			Sessions:    false,
			Memory:      true,
			History:     false,
			FileHistory: false,
			Config:      false,
		},
		Placeholders: defaultPlaceholders(),
	}

	err := export.Run(claudeHome, options)
	require.NoError(t, err)

	contents := readZipContents(t, outputPath)

	// Memory files must be present
	hasMemory := false
	for name := range contents {
		if strings.HasPrefix(name, "memory/") {
			hasMemory = true
			break
		}
	}
	assert.True(t, hasMemory, "memory files must be present when Memory category is enabled")

	// Sessions must NOT be present
	for name := range contents {
		if strings.HasPrefix(name, "sessions/") {
			t.Errorf("sessions entry %s must not be present when Sessions category is disabled", name)
		}
	}

	// History must NOT be present
	assert.NotContains(t, contents, "history/history.jsonl",
		"history must not be present when History category is disabled")

	// Config must NOT be present
	assert.NotContains(t, contents, "config.json",
		"config must not be present when Config category is disabled")

	// metadata.xml is always present.
	assert.Contains(t, contents, "metadata.xml")
}
