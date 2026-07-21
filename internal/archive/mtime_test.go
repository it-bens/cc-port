package archive_test

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
)

// zipMtimeRoundTripTolerance accounts for archive/zip's whole-second
// precision: the extended-timestamp extra field stores time as a uint32
// Unix-seconds value, and the legacy MS-DOS date has 2-second granularity.
const zipMtimeRoundTripTolerance = time.Second

// msDosEpoch is what archive/zip decodes a zero ModifiedDate/ModifiedTime
// pair to: msDosTimeToTime(0,0) yields the MS-DOS epoch base. Sink.createEntry
// omits any timestamp header when mtime is zero, so a zero-mtime write lands
// on this sentinel on read-back.
var msDosEpoch = time.Date(1979, 11, 30, 0, 0, 0, 0, time.UTC)

func TestSink_WriteBytesPreservesNonZeroMtime(t *testing.T) {
	// Whole-second mtime: archive/zip's encoding is whole-second, so a value
	// already at second boundary round-trips exactly.
	mtime := time.Date(2025, 6, 15, 12, 34, 56, 0, time.UTC)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	sink := archive.NewSink(writer, "claude", nil)

	_, err := sink.WriteBytes("data.bin", []byte("payload"), mtime)
	require.NoError(t, err, "WriteBytes")
	require.NoError(t, writer.Close(), "close zip")

	zipReader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	require.NoError(t, err, "open zip")
	require.Len(t, zipReader.File, 1, "one entry")

	got := zipReader.File[0].Modified
	require.WithinDuration(t, mtime, got, zipMtimeRoundTripTolerance,
		"FileHeader.Modified should round-trip at whole-second precision")
}

func TestSink_WriteBytesZeroMtimeProducesZeroModified(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	sink := archive.NewSink(writer, "claude", nil)

	_, err := sink.WriteBytes("data.bin", []byte("payload"), time.Time{})
	require.NoError(t, err, "WriteBytes")
	require.NoError(t, writer.Close(), "close zip")

	zipReader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	require.NoError(t, err, "open zip")
	require.Len(t, zipReader.File, 1, "one entry")

	got := zipReader.File[0].Modified
	require.True(t, got.Equal(msDosEpoch),
		"zero input should produce the MS-DOS-epoch Modified (%v), got %v", msDosEpoch, got)
}

func TestSink_WriteVerbatimPreservesNonZeroMtime(t *testing.T) {
	mtime := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	sink := archive.NewSink(writer, "claude", nil)

	_, err := sink.WriteVerbatim(context.Background(), "stream.bin", strings.NewReader("payload"), mtime)
	require.NoError(t, err, "WriteVerbatim")
	require.NoError(t, writer.Close(), "close zip")

	zipReader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	require.NoError(t, err, "open zip")
	require.Len(t, zipReader.File, 1, "one entry")
	require.WithinDuration(t, mtime, zipReader.File[0].Modified, zipMtimeRoundTripTolerance,
		"FileHeader.Modified should round-trip")
}

func TestSink_WriteJSONLPreservesNonZeroMtime(t *testing.T) {
	mtime := time.Date(2023, 11, 30, 23, 59, 58, 0, time.UTC)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	sink := archive.NewSink(writer, "claude", nil)

	_, err := sink.WriteJSONL(context.Background(), "data.jsonl",
		strings.NewReader("{\"a\":1}\n{\"a\":2}\n"), 1<<20, nil, mtime)
	require.NoError(t, err, "WriteJSONL")
	require.NoError(t, writer.Close(), "close zip")

	zipReader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	require.NoError(t, err, "open zip")
	require.Len(t, zipReader.File, 1, "one entry")
	require.WithinDuration(t, mtime, zipReader.File[0].Modified, zipMtimeRoundTripTolerance,
		"FileHeader.Modified should round-trip")
}
