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

func TestExport_PathAnonymization_OrderIndependent(t *testing.T) {
	// The anonymizer sorts placeholders by Original length descending so
	// nested paths (e.g. {{HOME}}=/Users/test is a prefix of
	// {{PROJECT_PATH}}=/Users/test/Projects/myproject) always resolve with
	// the most specific match winning. Caller-declared order must therefore
	// not affect the output — swap the Placeholder slice order and verify
	// byte-for-byte equality.
	claudeHome1 := testutil.SetupFixture(t)
	out1 := filepath.Join(t.TempDir(), "export-longer-first.zip")
	require.NoError(t, export.Run(claudeHome1, export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   out1,
		Categories:   export.CategorySet{Sessions: true, Memory: true, History: true, Config: true},
		Placeholders: defaultPlaceholders(),
	}))

	claudeHome2 := testutil.SetupFixture(t)
	out2 := filepath.Join(t.TempDir(), "export-shorter-first.zip")
	reversed := []export.Placeholder{
		{Key: "{{HOME}}", Original: "/Users/test"},
		{Key: "{{PROJECT_PATH}}", Original: fixtureProjectPath},
	}
	require.NoError(t, export.Run(claudeHome2, export.Options{
		ProjectPath:  fixtureProjectPath,
		OutputPath:   out2,
		Categories:   export.CategorySet{Sessions: true, Memory: true, History: true, Config: true},
		Placeholders: reversed,
	}))

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

	require.NoError(t, export.Run(claudeHome, export.Options{
		ProjectPath: fixtureProjectPath,
		OutputPath:  outputPath,
		Categories: export.CategorySet{
			Memory: true, Sessions: true, History: true, Config: true,
		},
		Placeholders: defaultPlaceholders(),
	}))

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
