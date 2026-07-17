package claude

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
)

func TestWriteJSONLFile_PreservesSourceMtime(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "source.jsonl")
	require.NoError(t, os.WriteFile(sourcePath, []byte("{\"a\":1}\n"), 0o600), "write source")

	expectedMtime := time.Date(2024, 3, 14, 15, 9, 26, 0, time.UTC)
	require.NoError(t, os.Chtimes(sourcePath, expectedMtime, expectedMtime), "set source mtime")

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	sink := archive.NewSink(writer, "claude", nil)

	_, err := writeJSONLFile(context.Background(), sink, "sessions/test.jsonl", sourcePath)
	require.NoError(t, err, "writeJSONLFile")
	require.NoError(t, writer.Close(), "close zip")

	zipReader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	require.NoError(t, err, "open zip")
	require.Len(t, zipReader.File, 1, "one entry")
	require.WithinDuration(t, expectedMtime, zipReader.File[0].Modified, time.Second,
		"writeJSONLFile should propagate source mtime to the archive")
}
