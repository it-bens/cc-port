package archive_test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
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

func TestRawEntries_RejectsUncleanName(t *testing.T) {
	body := buildZip(t, map[string]string{"claude/memory/../secret.jsonl": "payload"})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.DefaultCaps())
	require.NoError(t, err)

	_, err = reader.RawEntries()

	require.ErrorIs(t, err, archive.ErrZipSlip)
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

func TestOpenReader_RefusesTooManyEntries(t *testing.T) {
	const maxEntries = 5
	entries := make(map[string]string, maxEntries+1)
	for i := 0; i <= maxEntries; i++ {
		entries[fmt.Sprintf("claude/entry-%d", i)] = ""
	}
	body := buildZip(t, entries)

	_, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.Caps{
		MaxEntryBytes: 1 << 20, MaxAggregateBytes: 1 << 20, MaxEntries: maxEntries,
	})

	var countErr *archive.EntryCountCapError
	require.ErrorAs(t, err, &countErr)
	assert.Equal(t, maxEntries+1, countErr.Count)
	assert.Equal(t, maxEntries, countErr.Limit)
}

// TestOpenReader_RefusesDeclaredOverflowBeforeParsing proves the refusal
// above fires from the cheap End Of Central Directory trailer scan, before
// zip.NewReader's eager central-directory parse ever runs -- not from the
// parsed archive. buildBareEOCD below is not a parseable ZIP (it carries no
// central directory bytes, only the trailer); if the MaxEntries check ran
// after zip.NewReader instead of before it, this archive would fail with a
// generic "not a valid zip file" error instead of EntryCountCapError.
func TestOpenReader_RefusesDeclaredOverflowBeforeParsing(t *testing.T) {
	const maxEntries = 5
	const declaredEntries = maxEntries + 1

	body := buildBareEOCD(t, declaredEntries)

	_, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.Caps{
		MaxEntryBytes: 1 << 20, MaxAggregateBytes: 1 << 20, MaxEntries: maxEntries,
	})

	var countErr *archive.EntryCountCapError
	require.ErrorAs(t, err, &countErr)
	assert.Equal(t, declaredEntries, countErr.Count)
	assert.Equal(t, maxEntries, countErr.Limit)
}

// buildBareEOCD returns the 22-byte fixed portion of a ZIP End Of Central
// Directory record declaring entryCount entries, with no comment and no
// preceding central directory -- the whole "archive" is just this trailer.
func buildBareEOCD(t *testing.T, entryCount uint16) []byte {
	t.Helper()
	record := make([]byte, 22)
	binary.LittleEndian.PutUint32(record[0:4], 0x06054b50)   // EOCD signature
	binary.LittleEndian.PutUint16(record[8:10], entryCount)  // records on this disk
	binary.LittleEndian.PutUint16(record[10:12], entryCount) // total records
	return record
}

// TestOpenReader_RefusesTooManyDeclaredEntriesViaZip64 proves the zip64
// sentinel is resolved, not just skipped: a plain (non-zip64) EOCD's 16-bit
// entry-count field tops out at 65,535, so DefaultCaps' 200,000-entry
// MaxEntries can only ever be exceeded by a zip64 archive. Without
// resolving the sentinel, the early check could never fire against the cap
// this tool actually ships.
func TestOpenReader_RefusesTooManyDeclaredEntriesViaZip64(t *testing.T) {
	const maxEntries = 200_000
	const declaredEntries = maxEntries + 1

	body := buildBareZip64EOCD(t, declaredEntries)

	_, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.Caps{
		MaxEntryBytes: 1 << 20, MaxAggregateBytes: 1 << 20, MaxEntries: maxEntries,
	})

	var countErr *archive.EntryCountCapError
	require.ErrorAs(t, err, &countErr)
	assert.Equal(t, declaredEntries, countErr.Count)
	assert.Equal(t, maxEntries, countErr.Limit)
}

// buildBareZip64EOCD returns a synthetic trailer -- a zip64 End Of Central
// Directory record, the zip64 locator pointing at it, and a plain EOCD
// record carrying the zip64 sentinel -- declaring entryCount entries. Like
// buildBareEOCD, this is not a parseable ZIP; it exists only to drive
// declaredEntryCount's zip64 resolution path directly.
func buildBareZip64EOCD(t *testing.T, entryCount uint64) []byte {
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

func TestClassifyPresentKeys_FindsOnlyReferencedKeys(t *testing.T) {
	body := buildZip(t, map[string]string{
		"claude/one.txt": "references {{PROJECT_PATH}} only",
	})
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.DefaultCaps())
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)

	present, err := archive.ClassifyPresentKeys(
		context.Background(), entries, []string{"{{PROJECT_PATH}}", "{{HOME}}"}, archive.DefaultCaps().MaxAggregateBytes,
	)
	require.NoError(t, err)
	_, hasProjectPath := present["{{PROJECT_PATH}}"]
	_, hasHome := present["{{HOME}}"]
	assert.True(t, hasProjectPath)
	assert.False(t, hasHome)
}

// countdownContext cancels the wrapped context the moment its Err method
// has been consulted callsUntilCancel+1 times, rather than being canceled
// up front. This lets a test force cancellation to land at a specific point
// inside a scan deterministically (no wall-clock race, no dependence on
// scheduling) instead of only proving the trivial pre-canceled case.
type countdownContext struct {
	context.Context
	cancel           context.CancelFunc
	callsUntilCancel int
}

func (c *countdownContext) Err() error {
	if c.callsUntilCancel <= 0 {
		c.cancel()
	} else {
		c.callsUntilCancel--
	}
	return c.Context.Err()
}

// buildOrderedZip archives count entries named "claude/entry-<NN>.txt", each
// holding body, in a deterministic, index-ascending order (unlike buildZip's
// map, whose Go iteration order is randomized), so a countdown-context test
// can rely on which entry a given Err() call count lands on.
func buildOrderedZip(t *testing.T, count int, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for index := range count {
		entryWriter, err := writer.Create(fmt.Sprintf("claude/entry-%02d.txt", index))
		require.NoError(t, err)
		_, err = entryWriter.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

func openOrderedEntries(t *testing.T, body []byte) []archive.RawEntry {
	t.Helper()
	reader, err := archive.OpenReader(bytes.NewReader(body), int64(len(body)), archive.DefaultCaps())
	require.NoError(t, err)
	entries, err := reader.RawEntries()
	require.NoError(t, err)
	return entries
}

// TestClassifyPresentKeys_CancelsMidScan pins that a canceled context stops
// the per-entry classification walk partway through, rather than only
// checking ctx before the walk starts or after it finishes: callers that
// decompress and inspect archive bodies up to the aggregate cap (import
// preflight, pull planning) must be able to interrupt that cost. The
// budget is sized to exhaust while several, but not all, of ten entries
// have been opened, so this test exercises the per-entry check inside the
// loop specifically rather than the entry-time check before it.
func TestClassifyPresentKeys_CancelsMidScan(t *testing.T) {
	entries := openOrderedEntries(t, buildOrderedZip(t, 10, "no placeholder token here"))

	baseCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// Allows 4 non-canceled Err() calls: the entry check, then the first
	// three of ten entries. The 5th call (the fourth entry) triggers
	// cancel(), so entries 5-10 are never opened.
	ctx := &countdownContext{Context: baseCtx, cancel: cancel, callsUntilCancel: 4}

	present, err := archive.ClassifyPresentKeys(ctx, entries, []string{"{{PROJECT_PATH}}"}, archive.DefaultCaps().MaxAggregateBytes)

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, present, "a canceled context must not return partial classification results")
}

// TestClassifyPresentKeys_CancelsAfterFinalEntry pins the check after the
// scan loop: cancellation observed only once every entry has already
// passed its own per-entry check must still surface as an error, not a
// successful-looking result. The budget is sized to exhaust exactly on the
// call after the last of three entries, so every per-entry check inside
// the loop already succeeded and only the post-loop check can be
// responsible for the error this test asserts.
func TestClassifyPresentKeys_CancelsAfterFinalEntry(t *testing.T) {
	entries := openOrderedEntries(t, buildOrderedZip(t, 3, "no placeholder token here"))

	baseCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// Allows 4 non-canceled Err() calls: the entry check, then all three
	// entries. The 5th call — reached only after the loop has opened every
	// entry — is the post-loop check, which triggers cancel().
	ctx := &countdownContext{Context: baseCtx, cancel: cancel, callsUntilCancel: 4}

	present, err := archive.ClassifyPresentKeys(ctx, entries, []string{"{{PROJECT_PATH}}"}, archive.DefaultCaps().MaxAggregateBytes)

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, present, "a canceled context must not return a complete classification result")
}

// TestClassifyPresentKeys_CancelledContextOnNoEntriesReturnsError pins that
// a canceled context is never masked as "nothing present": with zero
// entries, the per-entry loop never runs, so this path depends on the
// entry-time and post-loop checks alone.
func TestClassifyPresentKeys_CancelledContextOnNoEntriesReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	present, err := archive.ClassifyPresentKeys(ctx, nil, []string{"{{PROJECT_PATH}}"}, archive.DefaultCaps().MaxAggregateBytes)

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, present, "a canceled context must not return a plausible-looking empty result")
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

	for _, relativePath := range []string{"..", "x/..", ".", "../../escaped.txt", "/abs/path", "x//y"} {
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
