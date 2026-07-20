package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/tool/codex"
)

// newTestToolSet mirrors newToolSet's registration but wires Codex through
// NewAdapter with a no-op ProcessLister, so a cmd-level test that opens the
// real default Codex home does not depend on whether a codex process
// happens to be running on the host executing the test: the process-table
// witness is the one evidence source that reads live machine state instead
// of the (already hermetic, temp-dir-scoped) Codex home a test controls.
func newTestToolSet() *tool.Set {
	return tool.NewSet(claude.New(), codex.NewAdapter(os.Getenv, noopProcessLister, time.Now))
}

func noopProcessLister() ([]codex.ProcessInfo, error) { return nil, nil }

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

// TestImportManifestCmd_RefusesArchiveDeclaringTooManyEntries proves the
// standalone "import manifest" subcommand enforces MaxEntries on its
// manifest read -- the one path that, before this change, called
// manifest.ReadManifestFromZip with no cap at all. The archive here is a
// trailer-only zip64 buffer declaring more entries than archive.DefaultCaps
// allows, so the test doesn't need to construct that many real entries.
func TestImportManifestCmd_RefusesArchiveDeclaringTooManyEntries(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "many-entries.zip")
	declaredEntries := uint64(archive.DefaultCaps().MaxEntries) + 1 //nolint:gosec // G115: DefaultCaps().MaxEntries is a positive constant
	require.NoError(t, os.WriteFile(archivePath, buildBareZip64EOCDArchive(t, declaredEntries), 0o600))
	cmd := newImportManifestCmd()
	require.NoError(t, cmd.Flags().Set("output", filepath.Join(t.TempDir(), "manifest.xml")))

	err := runImportManifest(cmd, []string{archivePath})

	var countErr *manifest.EntryCountCapError
	require.ErrorAs(t, err, &countErr)
	assert.Equal(t, archive.DefaultCaps().MaxEntries, countErr.Limit)
}

// buildBareZip64EOCDArchive returns a synthetic ZIP trailer -- a zip64 End
// Of Central Directory record, the zip64 locator pointing at it, and a
// plain EOCD record carrying the zip64 sentinel -- declaring entryCount
// entries. It is not a parseable ZIP; a plain (non-zip64) EOCD's 16-bit
// count field tops out at 65,535, well under DefaultCaps' 200,000-entry
// MaxEntries, so only a zip64 trailer can exercise this refusal at
// production scale.
func buildBareZip64EOCDArchive(t *testing.T, entryCount uint64) []byte {
	t.Helper()
	zip64End := make([]byte, 56)
	binary.LittleEndian.PutUint32(zip64End[0:4], 0x06064b50)   // zip64 EOCD signature
	binary.LittleEndian.PutUint64(zip64End[32:40], entryCount) // total entries

	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator[0:4], 0x07064b50) // zip64 locator signature
	binary.LittleEndian.PutUint64(locator[8:16], 0)         // zip64 EOCD record starts at offset 0

	eocd := make([]byte, 22)
	binary.LittleEndian.PutUint32(eocd[0:4], 0x06054b50) // EOCD signature
	binary.LittleEndian.PutUint16(eocd[10:12], 0xffff)   // zip64 sentinel

	return append(append(zip64End, locator...), eocd...)
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

// TestImportWarningsNameTheirToolDuringMultiToolImport drives the real
// import command over a Codex-only archive whose sole thread row is
// missing from the destination's state database: Finalize genuinely
// reports the "not ready" warning, and with claude also selected (so
// len(targets) > 1) the command must prefix it with Codex's DisplayName.
func TestImportWarningsNameTheirToolDuringMultiToolImport(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "codex-only.zip")
	require.NoError(t, os.WriteFile(archivePath, buildCodexOnlyArchive(t), 0o600))

	toolSet := newTestToolSet()
	flags := registerToolFlags(&cobra.Command{}, toolSet)
	flags.selected = []string{"claude", "codex"}
	*flags.homeOverrides["claude"] = filepath.Join(t.TempDir(), "unused-claude-home")
	*flags.homeOverrides["codex"] = t.TempDir() // no state_*.sqlite: the sidecar row cannot apply
	redirectProgressSink(t)

	cmd := newImportCmd(toolSet, flags)
	cmd.Flags().Bool("quiet", true, "")
	cmd.Flags().Bool("verbose", false, "")
	cmd.Flags().Bool("debug", false, "")
	cmd.Flags().Bool("json", false, "")
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetContext(t.Context())
	cmd.SetArgs([]string{archivePath, filepath.Join(t.TempDir(), "new-project")})

	require.NoError(t, cmd.Execute())

	assert.Contains(t, stderr.String(), "Warning (OpenAI Codex): 1 threads sidecar row(s) could not be applied "+
		"because Codex has not created their thread rows yet; rerun import after opening the project\n")
}

// redirectProgressSink swaps the package-level progress output sink to a
// temp file for the duration of the test, so a real cmd.Execute() run does
// not print its quiet-mode "done" line to the actual test process's stderr.
func redirectProgressSink(t *testing.T) {
	t.Helper()
	sink, err := os.Create(filepath.Join(t.TempDir(), "progress.log"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	original := stderrSink
	stderrSink = sink
	t.Cleanup(func() { stderrSink = original })
}

// buildCodexOnlyArchive exports the default Codex fixture, whose state
// database backs exactly one thread row, into a single-tool archive via
// the generic export.Run path (not the export CLI command), so the
// import test above isolates the import command's own behavior.
func buildCodexOnlyArchive(t *testing.T) []byte {
	t.Helper()

	sourceHome := codex.SetupFixture(t)
	workspace := codex.NewWorkspace(
		sourceHome,
		func(string) string { return "" },
		func() ([]codex.ProcessInfo, error) { return nil, nil },
		time.Now,
	)
	codexTool := codex.New()
	selected := make(map[string]bool)
	for _, category := range codexTool.Categories() {
		selected[category.Name] = true
	}
	placeholders, err := workspace.Placeholders(codex.FixtureProjectPath(), selected)
	require.NoError(t, err)

	var archiveBytes bytes.Buffer
	_, err = export.Run(t.Context(), []tool.Target{{Tool: codexTool, Workspace: workspace}}, &export.Options{
		ProjectPath:  codex.FixtureProjectPath(),
		Output:       &archiveBytes,
		Selected:     map[string]map[string]bool{codexTool.Name(): selected},
		Placeholders: map[string][]manifest.Placeholder{codexTool.Name(): placeholders},
	})
	require.NoError(t, err)
	return archiveBytes.Bytes()
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
