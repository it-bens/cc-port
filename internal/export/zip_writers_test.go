package export

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// archive/zip writes only whole-second precision: the extended-timestamp
// extra field stores time as uint32 Unix seconds (writer.go ~L325), and
// the legacy MS-DOS date has 2-second granularity. Sub-second precision
// in fh.Modified is therefore lost on round-trip; the helper round-trips
// mtime truncated to whole seconds.
const zipMtimeRoundTripTolerance = time.Second

// archive/zip's Create() produces an entry with both ModifiedDate=0 and
// ModifiedTime=0 and no extra timestamp. On read-back, msDosTimeToTime(0,0)
// yields 1979-11-30 00:00:00 UTC — the MS-DOS epoch base. We use that as the
// sentinel for "no mtime carried."
var msDosEpoch = time.Date(1979, 11, 30, 0, 0, 0, 0, time.UTC)

func TestWriteToZip_PreservesNonZeroMtime(t *testing.T) {
	// Whole-second mtime: archive/zip's encoding is whole-second, so a value
	// already at second boundary round-trips exactly.
	mtime := time.Date(2025, 6, 15, 12, 34, 56, 0, time.UTC)
	buffer := &bytes.Buffer{}
	zipWriter := zip.NewWriter(buffer)

	_, err := writeToZip(zipWriter, "data.bin", []byte("payload"), mtime)
	require.NoError(t, err, "writeToZip")
	require.NoError(t, zipWriter.Close(), "close zip")

	zipReader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	require.NoError(t, err, "open zip")
	require.Len(t, zipReader.File, 1, "one entry")

	got := zipReader.File[0].Modified
	require.WithinDuration(t, mtime, got, zipMtimeRoundTripTolerance,
		"FileHeader.Modified should round-trip at whole-second precision")
}

func TestWriteToZip_ZeroMtimeProducesZeroModified(t *testing.T) {
	buffer := &bytes.Buffer{}
	zipWriter := zip.NewWriter(buffer)

	_, err := writeToZip(zipWriter, "data.bin", []byte("payload"), time.Time{})
	require.NoError(t, err, "writeToZip")
	require.NoError(t, zipWriter.Close(), "close zip")

	zipReader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	require.NoError(t, err, "open zip")
	require.Len(t, zipReader.File, 1, "one entry")

	got := zipReader.File[0].Modified
	require.True(t, got.Equal(msDosEpoch),
		"zero input should produce the MS-DOS-epoch Modified (%v), got %v",
		msDosEpoch, got)
}

func TestWriteReaderToZip_PreservesNonZeroMtime(t *testing.T) {
	mtime := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	buffer := &bytes.Buffer{}
	zipWriter := zip.NewWriter(buffer)

	_, err := writeReaderToZip(context.Background(), zipWriter, "stream.bin",
		strings.NewReader("payload"), mtime)
	require.NoError(t, err, "writeReaderToZip")
	require.NoError(t, zipWriter.Close(), "close zip")

	zipReader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	require.NoError(t, err, "open zip")
	require.Len(t, zipReader.File, 1, "one entry")
	require.WithinDuration(t, mtime, zipReader.File[0].Modified, zipMtimeRoundTripTolerance,
		"FileHeader.Modified should round-trip")
}

func TestWriteJSONLToZip_PreservesNonZeroMtime(t *testing.T) {
	mtime := time.Date(2023, 11, 30, 23, 59, 58, 0, time.UTC)
	buffer := &bytes.Buffer{}
	zipWriter := zip.NewWriter(buffer)

	_, err := writeJSONLToZip(context.Background(), zipWriter, "data.jsonl",
		strings.NewReader("{\"a\":1}\n{\"a\":2}\n"), nil, mtime)
	require.NoError(t, err, "writeJSONLToZip")
	require.NoError(t, zipWriter.Close(), "close zip")

	zipReader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	require.NoError(t, err, "open zip")
	require.Len(t, zipReader.File, 1, "one entry")
	require.WithinDuration(t, mtime, zipReader.File[0].Modified, zipMtimeRoundTripTolerance,
		"FileHeader.Modified should round-trip")
}
