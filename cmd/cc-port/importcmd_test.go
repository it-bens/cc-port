package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/tool/codex"
)

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

	require.ErrorIs(t, err, errOutputExists)
}

func TestImportCmd_PassphraseFlagsRegistered(t *testing.T) {
	toolSet := newToolSet()
	flags := registerToolFlags(&cobra.Command{}, toolSet)
	cmd := newImportCmd(toolSet, flags)
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

func TestImportWarningsNameTheirToolDuringMultiToolImport(t *testing.T) {
	var stderr bytes.Buffer
	targets := []tool.Target{{Tool: claude.New()}, {Tool: codex.New()}}

	err := renderImportWarnings(&stderr, targets, map[string][]string{
		"codex": {"1 thread row is not ready; rerun import after opening the project"},
	})

	require.NoError(t, err)
	assert.Equal(
		t, "Warning (OpenAI Codex): 1 thread row is not ready; rerun import after opening the project\n", stderr.String(),
	)
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
		Created: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data, err := xml.Marshal(metadata)
	require.NoError(t, err)
	_, err = entry.Write(append([]byte(xml.Header), data...))
	require.NoError(t, err)
	require.NoError(t, zipWriter.Close())
	require.NoError(t, archiveFile.Close())

	return archivePath
}
