package archive_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
)

func buildZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, body := range entries {
		entryWriter, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entryWriter.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

func TestOpenReader_NilSourceFails(t *testing.T) {
	_, err := archive.OpenReader(nil, 0)
	require.ErrorIs(t, err, archive.ErrNilSource)
}

func TestRawEntries_SplitsToolPrefixAndSkipsMetadata(t *testing.T) {
	body := buildZip(t, map[string]string{
		"metadata.xml":            "<cc-port/>",
		"claude/sessions/a.jsonl": "{}",
	})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)

	entries, err := reader.RawEntries()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "claude", entries[0].ToolName)
	assert.Equal(t, "sessions/a.jsonl", entries[0].Entry.Name)
}

func TestRawEntries_RejectsMissingToolPrefix(t *testing.T) {
	body := buildZip(t, map[string]string{"unprefixed.txt": "x"})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)

	_, err = reader.RawEntries()
	require.ErrorIs(t, err, archive.ErrMalformedEntryName)
}

func TestEntry_ReadAll_RejectsOverEntryCap(t *testing.T) {
	restore := archive.SetCaps(archive.Caps{MaxEntryBytes: 4, MaxAggregateBytes: 1 << 20})
	defer restore()

	body := buildZip(t, map[string]string{"claude/big.txt": "way too many bytes"})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)
	require.Len(t, entries, 1)

	_, err = entries[0].Entry.ReadAll()
	require.ErrorIs(t, err, archive.ErrEntryCapExceeded)
}

func TestClassifyPresentKeys_FindsOnlyReferencedKeys(t *testing.T) {
	body := buildZip(t, map[string]string{
		"claude/one.txt": "references {{PROJECT_PATH}} only",
	})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)

	present, err := archive.ClassifyPresentKeys(entries, []string{"{{PROJECT_PATH}}", "{{HOME}}"})
	require.NoError(t, err)
	_, hasProjectPath := present["{{PROJECT_PATH}}"]
	_, hasHome := present["{{HOME}}"]
	assert.True(t, hasProjectPath)
	assert.False(t, hasHome)
}

func TestResolvePlaceholdersStream_SubstitutesLongestMatchFirst(t *testing.T) {
	var out bytes.Buffer
	err := archive.ResolvePlaceholdersStream(
		strings.NewReader("path is {{PROJECT_PATH}} end"),
		&out,
		map[string]string{"{{PROJECT_PATH}}": "/new/path", "{{PROJECT}}": "/wrong"},
	)
	require.NoError(t, err)
	assert.Equal(t, "path is /new/path end", out.String())
}

func TestValidateResolutions_RejectsEmptyAndRelative(t *testing.T) {
	require.Error(t, archive.ValidateResolutions(map[string]string{"{{X}}": ""}))
	require.Error(t, archive.ValidateResolutions(map[string]string{"{{X}}": "relative/path"}))
	require.NoError(t, archive.ValidateResolutions(map[string]string{"{{X}}": "/absolute/path"}))
}

func TestStageSibling_RejectsZipSlip(t *testing.T) {
	body := buildZip(t, map[string]string{"claude/evil.txt": "payload"})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)

	baseDir := t.TempDir()
	_, _, err = archive.StageSibling(baseDir, "../../escaped.txt", entries[0].Entry, nil, 0o600, entries[0].Entry.Modified)
	require.Error(t, err)
}

func TestStageSibling_StreamsBodyAndResolvesPlaceholders(t *testing.T) {
	body := buildZip(t, map[string]string{"claude/note.txt": "home is {{HOME}}"})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)

	baseDir := t.TempDir()
	staged, bytesRead, err := archive.StageSibling(
		baseDir, "note.txt", entries[0].Entry, map[string]string{"{{HOME}}": "/Users/test"}, 0o600, entries[0].Entry.Modified,
	)
	require.NoError(t, err)
	assert.Positive(t, bytesRead)

	written, err := os.ReadFile(staged.Temp)
	require.NoError(t, err)
	assert.Equal(t, "home is /Users/test", string(written))
	assert.Equal(t, filepath.Join(baseDir, "note.txt"), staged.Final)
}

func TestSink_WriteBytesAppliesPlaceholdersAndPrefix(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	sink := archive.NewSink(writer, "claude", []manifest.Placeholder{
		{Key: "{{PROJECT_PATH}}", Original: "/Users/test/myproject"},
	})

	written, err := sink.WriteBytes("config.json", []byte(`{"path":"/Users/test/myproject"}`), time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "config.json", written.Name)
	require.NoError(t, writer.Close())

	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	require.Len(t, zipReader.File, 1)
	assert.Equal(t, "claude/config.json", zipReader.File[0].Name)

	rc, err := zipReader.File[0].Open()
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	var out bytes.Buffer
	_, err = out.ReadFrom(rc)
	require.NoError(t, err)
	assert.JSONEq(t, `{"path":"{{PROJECT_PATH}}"}`, out.String())
}

func TestSink_WriteJSONL_DropsLineWhenTransformReturnsNil(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	sink := archive.NewSink(writer, "claude", nil)

	_, err := sink.WriteJSONL(context.Background(), "history.jsonl", strings.NewReader("keep\nDROP\nkeep2\n"), 1024,
		func(line []byte) []byte {
			if string(line) == "DROP" {
				return nil
			}
			return line
		}, time.Time{})
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	rc, err := zipReader.File[0].Open()
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	var out bytes.Buffer
	_, err = out.ReadFrom(rc)
	require.NoError(t, err)
	assert.Equal(t, "keep\nkeep2\n", out.String())
}

func TestSink_WriteJSONL_AcceptsLineBelowScannerCap(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	sink := archive.NewSink(writer, "claude", nil)

	_, err := sink.WriteJSONL(context.Background(), "history.jsonl", strings.NewReader("1234"), 5, nil, time.Time{})

	require.NoError(t, err)
	require.NoError(t, writer.Close())
}

func TestSink_WriteJSONL_RejectsInteriorLineAtScannerCap(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	sink := archive.NewSink(writer, "claude", nil)

	_, err := sink.WriteJSONL(context.Background(), "history.jsonl", strings.NewReader("12345\nmore"), 5, nil, time.Time{})

	require.ErrorIs(t, err, bufio.ErrTooLong)
}
