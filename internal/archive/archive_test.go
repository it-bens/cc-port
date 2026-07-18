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
	_, err := archive.OpenReader(nil, 0, archive.DefaultCaps())
	require.ErrorIs(t, err, archive.ErrNilSource)
}

func TestRawEntries_SplitsToolPrefixAndSkipsMetadata(t *testing.T) {
	body := buildZip(t, map[string]string{
		"metadata.xml":            "<cc-port/>",
		"claude/sessions/a.jsonl": "{}",
	})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.DefaultCaps())
	require.NoError(t, err)

	entries, err := reader.RawEntries()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "claude", entries[0].ToolName)
	assert.Equal(t, "sessions/a.jsonl", entries[0].Entry.Name)
}

func TestRawEntries_RejectsMissingToolPrefix(t *testing.T) {
	body := buildZip(t, map[string]string{"unprefixed.txt": "x"})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.DefaultCaps())
	require.NoError(t, err)

	_, err = reader.RawEntries()
	require.ErrorIs(t, err, archive.ErrMalformedEntryName)
}

func TestEntry_ReadAll_RejectsOverEntryCap(t *testing.T) {
	body := buildZip(t, map[string]string{"claude/big.txt": "way too many bytes"})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.Caps{MaxEntryBytes: 4, MaxAggregateBytes: 1 << 20})
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
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.DefaultCaps())
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)

	present, err := archive.ClassifyPresentKeys(entries, []string{"{{PROJECT_PATH}}", "{{HOME}}"}, archive.DefaultCaps().MaxAggregateBytes)
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

func TestSink_ApplyPlaceholdersSubstitutesNestedValuesLongestFirst(t *testing.T) {
	var buf bytes.Buffer
	sink := archive.NewSink(zip.NewWriter(&buf), "claude", []manifest.Placeholder{
		{Key: "{{HOME}}", Original: "/Users/test"},
		{Key: "{{PROJECT_PATH}}", Original: "/Users/test/project"},
	})

	got := sink.ApplyPlaceholders([]byte("cwd=/Users/test/project/file.go home=/Users/test/.claude"))

	assert.Equal(t, "cwd={{PROJECT_PATH}}/file.go home={{HOME}}/.claude", string(got))
}

func TestValidateResolutions_RejectsEmptyAndRelative(t *testing.T) {
	for name, value := range map[string]string{"empty": "", "relative": "relative/path"} {
		t.Run(name, func(t *testing.T) {
			err := archive.ValidateResolutions(map[string]string{"{{X}}": value})
			var invalid *archive.InvalidResolutionsError
			require.ErrorAs(t, err, &invalid)
			assert.Equal(t, []string{"{{X}}"}, invalid.Keys)
		})
	}
	require.NoError(t, archive.ValidateResolutions(map[string]string{"{{X}}": "/absolute/path"}))
}

func TestStageSibling_RejectsZipSlip(t *testing.T) {
	body := buildZip(t, map[string]string{"claude/evil.txt": "payload"})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.DefaultCaps())
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)

	for _, relativePath := range []string{"..", "x/..", ".", "../../escaped.txt"} {
		t.Run(relativePath, func(t *testing.T) {
			_, _, stageErr := archive.StageSibling(
				t.TempDir(), relativePath, entries[0].Entry, nil, 0o600, entries[0].Entry.Modified,
			)

			require.ErrorIs(t, stageErr, archive.ErrZipSlip)
		})
	}
}

func TestStageSibling_StreamsBodyAndResolvesPlaceholders(t *testing.T) {
	body := buildZip(t, map[string]string{"claude/note.txt": "home is {{HOME}}"})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.DefaultCaps())
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

func TestStageSibling_RejectsExpandedEntryOverCap(t *testing.T) {
	body := buildZip(t, map[string]string{"claude/note.txt": "{{X}}"})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.Caps{MaxEntryBytes: 8, MaxAggregateBytes: 64})
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)

	_, _, err = archive.StageSibling(
		t.TempDir(), "note.txt", entries[0].Entry, map[string]string{"{{X}}": "/expanded/"}, 0o600, entries[0].Entry.Modified,
	)

	require.ErrorIs(t, err, archive.ErrEntryCapExceeded)
}

func TestStageSibling_AggregateCapCountsDecodedBytes(t *testing.T) {
	body := buildZip(t, map[string]string{
		"claude/one.txt":   strings.Repeat("{{X}}", 3),
		"claude/two.txt":   strings.Repeat("{{X}}", 3),
		"claude/three.txt": strings.Repeat("{{X}}", 3),
	})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.Caps{MaxEntryBytes: 16, MaxAggregateBytes: 32})
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)
	counter := archive.NewAggregateCounter(32)

	for _, raw := range entries {
		_, _, err = archive.StageSibling(
			t.TempDir(), raw.Entry.Name, raw.Entry.WithAggregateCounter(counter),
			map[string]string{"{{X}}": ""}, 0o600, raw.Entry.Modified,
		)
		if err != nil {
			break
		}
	}

	require.ErrorIs(t, err, archive.ErrAggregateCapExceeded)
}

func TestResolveEntryBytes_AggregateCapCountsDecodedInput(t *testing.T) {
	body := buildZip(t, map[string]string{"claude/history.jsonl": "{{X}}"})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.Caps{MaxEntryBytes: 16, MaxAggregateBytes: 9})
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)
	counter := archive.NewAggregateCounter(9)

	resolved, err := archive.ResolveEntryBytes(entries[0].Entry.WithAggregateCounter(counter), map[string]string{"{{X}}": "/expanded/"})

	require.NoError(t, err)
	assert.Equal(t, []byte("/expanded/"), resolved)
}

func TestEntryReadAll_AggregateCapStopsBeforeWholeEntryDecodes(t *testing.T) {
	input := strings.Repeat("x", 128<<10)
	body := buildZip(t, map[string]string{"claude/note.txt": input})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.Caps{MaxEntryBytes: 256 << 10, MaxAggregateBytes: 64})
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)

	_, err = entries[0].Entry.WithAggregateCounter(archive.NewAggregateCounter(64)).ReadAll()

	var capErr *archive.AggregateCapError
	require.ErrorAs(t, err, &capErr)
	assert.Greater(t, capErr.Bytes, int64(64))
	assert.Less(t, capErr.Bytes, int64(len(input)))
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

func TestSink_WriteJSONL_RejectsFinalLineAtScannerCap(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	sink := archive.NewSink(writer, "claude", nil)

	_, err := sink.WriteJSONL(context.Background(), "history.jsonl", strings.NewReader("12345"), 5, nil, time.Time{})

	require.ErrorIs(t, err, bufio.ErrTooLong)
}
