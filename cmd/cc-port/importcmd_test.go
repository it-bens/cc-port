package main

import (
	"archive/zip"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
)

func TestParseResolutionFlags_Empty(t *testing.T) {
	parsed, err := parseResolutionFlags(nil)
	require.NoError(t, err)
	assert.Empty(t, parsed)
}

func TestParseResolutionFlags_SingleEntry(t *testing.T) {
	parsed, err := parseResolutionFlags([]string{"{{HOME}}=/Users/me"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"{{HOME}}": "/Users/me"}, parsed)
}

func TestParseResolutionFlags_MultipleEntries(t *testing.T) {
	parsed, err := parseResolutionFlags([]string{
		"{{HOME}}=/Users/me",
		"{{WORK}}=/opt/work",
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"{{HOME}}": "/Users/me",
		"{{WORK}}": "/opt/work",
	}, parsed)
}

func TestParseResolutionFlags_ValueWithEquals(t *testing.T) {
	// Only the first '=' splits key from value; subsequent ones are preserved.
	parsed, err := parseResolutionFlags([]string{"{{URL}}=/opt/app?x=1"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"{{URL}}": "/opt/app?x=1"}, parsed)
}

func TestParseResolutionFlags_RejectsMissingEquals(t *testing.T) {
	_, err := parseResolutionFlags([]string{"{{HOME}}"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KEY=VALUE")
}

func TestParseResolutionFlags_RejectsEmptyKey(t *testing.T) {
	_, err := parseResolutionFlags([]string{"=/Users/me"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty key")
}

func TestParseResolutionFlags_RejectsProjectPath(t *testing.T) {
	_, err := parseResolutionFlags([]string{"{{PROJECT_PATH}}=/Users/me/project"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "{{PROJECT_PATH}}")
}

func TestParseResolutionFlags_RejectsDuplicateKey(t *testing.T) {
	_, err := parseResolutionFlags([]string{
		"{{HOME}}=/Users/alice",
		"{{HOME}}=/Users/bob",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "{{HOME}}")
}

func TestParseResolutionFlags_SuggestsManifestWorkflow(t *testing.T) {
	_, err := parseResolutionFlags([]string{"bare-no-equals"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no '=' found")
	assert.Contains(t, err.Error(), "cc-port import manifest")
}

func TestImportManifestCmd_HasOutputFlag(t *testing.T) {
	cmd := newImportManifestCmd()
	flag := cmd.Flags().Lookup("output")
	require.NotNil(t, flag, "import manifest --output must be registered")
	short := cmd.Flags().ShorthandLookup("o")
	require.NotNil(t, short, "import manifest -o must be registered")
	assert.Equal(t, "manifest.xml", flag.DefValue)
}

func TestImportManifestCmd_OverwriteGuard(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "pre-existing.xml")
	require.NoError(t, os.WriteFile(outPath, []byte("x"), 0o600))
	cmd := newImportManifestCmd()
	require.NoError(t, cmd.Flags().Set("output", outPath))
	archivePath := buildMinimalArchive(t)

	err := runImportManifest(cmd, []string{archivePath})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestImportCmd_PassphraseFlagsRegistered(t *testing.T) {
	var claudeDir string
	cmd := newImportCmd(&claudeDir)
	require.NotNil(t, cmd.Flags().Lookup("passphrase-env"),
		"--passphrase-env should be registered on import")
	require.NotNil(t, cmd.Flags().Lookup("passphrase-file"),
		"--passphrase-file should be registered on import")
}

func TestImportManifestCmd_PassphraseFlagsRegistered(t *testing.T) {
	cmd := newImportManifestCmd()
	require.NotNil(t, cmd.Flags().Lookup("passphrase-env"),
		"--passphrase-env should be registered on import manifest")
	require.NotNil(t, cmd.Flags().Lookup("passphrase-file"),
		"--passphrase-file should be registered on import manifest")
}

// buildMinimalArchive writes a zip archive containing a valid metadata.xml
// entry and returns the archive path. The zip entry name must be
// "metadata.xml" because that is the name ReadManifestFromZip searches for.
func buildMinimalArchive(t *testing.T) string {
	t.Helper()

	archivePath := filepath.Join(t.TempDir(), "minimal.zip")
	archiveFile, err := os.Create(archivePath) //nolint:gosec // G304: test-controlled temp path
	require.NoError(t, err)
	t.Cleanup(func() { _ = archiveFile.Close() })

	zipWriter := zip.NewWriter(archiveFile)
	entry, err := zipWriter.Create("metadata.xml")
	require.NoError(t, err)

	metadata := &manifest.Metadata{
		Export: manifest.Info{
			Created:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Categories: []manifest.Category{},
		},
		Placeholders: []manifest.Placeholder{},
	}
	data, err := xml.Marshal(metadata)
	require.NoError(t, err)
	_, err = entry.Write(append([]byte(xml.Header), data...))
	require.NoError(t, err)
	require.NoError(t, zipWriter.Close())
	require.NoError(t, archiveFile.Close())

	return archivePath
}
