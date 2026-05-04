package manifest_test

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
)

func TestMetadata_MarshalUnmarshal(t *testing.T) {
	created := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)

	original := &manifest.Metadata{
		Export: manifest.Info{
			Created: created,
			Categories: []manifest.Category{
				{Name: "sessions", Included: true},
				{Name: "settings", Included: false},
			},
		},
		Placeholders: []manifest.Placeholder{
			{Key: "HOME", Original: "/home/user"},
			{Key: "PROJECT", Original: "/home/user/project"},
		},
	}

	temporaryDirectory := t.TempDir()
	path := filepath.Join(temporaryDirectory, "metadata.xml")

	if err := manifest.WriteManifest(path, original); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	roundTripped, err := manifest.ReadManifest(path)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}

	opts := cmp.Options{
		cmpopts.IgnoreFields(manifest.Metadata{}, "XMLName"),
		cmpopts.EquateApproxTime(time.Second),
	}

	if diff := cmp.Diff(original, roundTripped, opts); diff != "" {
		t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestMetadata_XMLFormat(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	metadata := &manifest.Metadata{
		Export: manifest.Info{
			Created: created,
			Categories: []manifest.Category{
				{Name: "sessions", Included: true},
			},
		},
		Placeholders: []manifest.Placeholder{
			{Key: "HOME", Original: "/home/user"},
		},
	}

	path := filepath.Join(t.TempDir(), "metadata.xml")

	require.NoError(t, manifest.WriteManifest(path, metadata))

	data, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled temp path
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, xml.Header[:5])
	assert.Contains(t, content, "<cc-port")
	assert.Contains(t, content, "<export>")
	assert.Contains(t, content, "<categories>")
	assert.Contains(t, content, `name="sessions"`)
	assert.Contains(t, content, `included="true"`)
	assert.Contains(t, content, "<placeholders>")
	assert.Contains(t, content, "<placeholder")
	assert.Contains(t, content, `key="HOME"`)
	assert.Contains(t, content, `original="/home/user"`)
}

func TestManifest_PlaceholderFieldsSurviveXMLRoundTrip(t *testing.T) {
	created := time.Date(2024, 3, 20, 12, 0, 0, 0, time.UTC)

	original := &manifest.Metadata{
		Export: manifest.Info{
			Created:    created,
			Categories: []manifest.Category{},
		},
		Placeholders: []manifest.Placeholder{
			{
				Key:      "HOME",
				Original: "/home/olduser",
				Resolve:  "/home/newuser",
			},
			{
				Key:      "PLAIN",
				Original: "/plain/path",
			},
		},
	}

	path := filepath.Join(t.TempDir(), "metadata.xml")

	require.NoError(t, manifest.WriteManifest(path, original))
	roundTripped, err := manifest.ReadManifest(path)
	require.NoError(t, err)

	assert.Equal(t, "/home/newuser", roundTripped.Placeholders[0].Resolve,
		"non-empty Resolve must round-trip")
	assert.Empty(t, roundTripped.Placeholders[1].Resolve,
		"omitted Resolve must round-trip as empty string")
}

func TestManifest_MetadataSurvivesZIPRoundTrip(t *testing.T) {
	created := time.Date(2024, 3, 20, 12, 0, 0, 0, time.UTC)

	original := &manifest.Metadata{
		Export: manifest.Info{
			Created:    created,
			Categories: []manifest.Category{},
		},
		Placeholders: []manifest.Placeholder{
			{
				Key:      "HOME",
				Original: "/home/olduser",
				Resolve:  "/home/newuser",
			},
			{
				Key:      "PLAIN",
				Original: "/plain/path",
			},
		},
	}

	temporaryDirectory := t.TempDir()
	path := filepath.Join(temporaryDirectory, "metadata.xml")

	require.NoError(t, manifest.WriteManifest(path, original))

	opts := cmp.Options{
		cmpopts.IgnoreFields(manifest.Metadata{}, "XMLName"),
		cmpopts.EquateApproxTime(time.Second),
		cmpopts.EquateEmpty(),
	}

	assertZIPRoundTrip(t, original, temporaryDirectory, path, opts)
}

// assertZIPRoundTrip verifies the metadata survives a ZIP round-trip.
func assertZIPRoundTrip(t *testing.T, original *manifest.Metadata, temporaryDirectory, path string, opts cmp.Options) {
	t.Helper()

	zipPath := filepath.Join(temporaryDirectory, "export.zip")
	if err := createTestZip(zipPath, path); err != nil {
		t.Fatalf("createTestZip: %v", err)
	}

	zipFile, err := os.Open(zipPath) //nolint:gosec // G304: test-controlled temp path
	require.NoError(t, err, "open zip")
	t.Cleanup(func() { _ = zipFile.Close() })
	zipInfo, err := zipFile.Stat()
	require.NoError(t, err, "stat zip")

	fromZip, err := manifest.ReadManifestFromZip(zipFile, zipInfo.Size())
	if err != nil {
		t.Fatalf("ReadManifestFromZip: %v", err)
	}

	if diff := cmp.Diff(original, fromZip, opts); diff != "" {
		t.Errorf("ZIP round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestReadManifest_RejectsOversizedFile(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "oversize.xml")

	oversize := bytes.Repeat([]byte("x"), 5<<20)
	require.NoError(t, os.WriteFile(path, oversize, 0o600))

	_, err := manifest.ReadManifest(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestReadManifestFromZip_RejectsOversizedEntry(t *testing.T) {
	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "oversize.zip")

	archiveFile, err := os.Create(archivePath) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	zipWriter := zip.NewWriter(archiveFile)

	entry, err := zipWriter.Create("metadata.xml")
	require.NoError(t, err)
	// 5 MiB of XML-safe padding inside a <placeholders> block. Above the
	// 4 MiB manifest cap, so ReadManifestFromZip must reject it.
	headerTxt := xml.Header +
		`<cc-port><export>` +
		`<created>2026-01-01T00:00:00Z</created>` +
		`<categories></categories></export><placeholders>`
	header := []byte(headerTxt)
	padding := bytes.Repeat([]byte("<placeholder key=\"K\" original=\"V\"/>"), 5<<20/40)
	footer := []byte(`</placeholders></cc-port>`)
	_, err = entry.Write(append(append(header, padding...), footer...))
	require.NoError(t, err)
	require.NoError(t, zipWriter.Close())
	require.NoError(t, archiveFile.Close())

	zipFile, err := os.Open(archivePath) //nolint:gosec // G304: test-controlled temp path
	require.NoError(t, err, "open zip")
	t.Cleanup(func() { _ = zipFile.Close() })
	zipInfo, err := zipFile.Stat()
	require.NoError(t, err, "stat zip")

	_, err = manifest.ReadManifestFromZip(zipFile, zipInfo.Size())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestMetadata_SyncFieldsRoundTrip(t *testing.T) {
	created := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)

	original := &manifest.Metadata{
		Export: manifest.Info{
			Created:    created,
			Categories: []manifest.Category{},
		},
		SyncPushedBy: "alice@example.com",
		SyncPushedAt: "2026-04-25T14:32:11Z",
	}

	path := filepath.Join(t.TempDir(), "metadata.xml")

	require.NoError(t, manifest.WriteManifest(path, original))
	roundTripped, err := manifest.ReadManifest(path)
	require.NoError(t, err)

	assert.Equal(t, "alice@example.com", roundTripped.SyncPushedBy)
	assert.Equal(t, "2026-04-25T14:32:11Z", roundTripped.SyncPushedAt)
}

func TestMetadata_OmitsSyncFieldsWhenEmpty(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	metadata := &manifest.Metadata{
		Export: manifest.Info{
			Created:    created,
			Categories: []manifest.Category{},
		},
	}

	path := filepath.Join(t.TempDir(), "metadata.xml")

	require.NoError(t, manifest.WriteManifest(path, metadata))

	data, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled temp path
	require.NoError(t, err)
	content := string(data)

	assert.NotContains(t, content, "<sync-pushed-by")
	assert.NotContains(t, content, "<sync-pushed-at")
}

// createTestZip creates a ZIP archive at zipPath containing the file at
// sourcePath stored as metadata.xml.
func createTestZip(zipPath, sourcePath string) error {
	zipFile, err := os.Create(zipPath) //nolint:gosec // G304: test-controlled temp path
	if err != nil {
		return err
	}
	defer func() { _ = zipFile.Close() }()

	writer := zip.NewWriter(zipFile)
	defer func() { _ = writer.Close() }()

	entry, err := writer.Create("metadata.xml")
	if err != nil {
		return err
	}

	data, err := os.ReadFile(sourcePath) //nolint:gosec // G304: test-controlled path
	if err != nil {
		return err
	}

	_, err = entry.Write(data)
	return err
}

func TestReadManifestFromZip_NilSrc(t *testing.T) {
	_, err := manifest.ReadManifestFromZip(nil, 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "MaterializeStage",
		"error must hint at the missing pipeline stage to ease debugging")
}
