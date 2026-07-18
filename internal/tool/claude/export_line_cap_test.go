package claude

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
)

func TestWriteJSONLFile_ExportsLegalLongHistoryLine(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "history.jsonl")
	require.NoError(t, os.WriteFile(sourcePath, []byte(strings.Repeat("x", MaxHistoryLine-1)+"\n"), 0o600))
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	sink := archive.NewSink(writer, "claude", nil)

	_, err := writeJSONLFile(context.Background(), sink, "history/history.jsonl", sourcePath)

	require.NoError(t, err)
}

func TestWriteJSONLFile_RejectsHistoryLineOverLimit(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "history.jsonl")
	require.NoError(t, os.WriteFile(sourcePath, []byte(strings.Repeat("x", MaxHistoryLine+1)), 0o600))
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	sink := archive.NewSink(writer, "claude", nil)

	_, err := writeJSONLFile(context.Background(), sink, "history/history.jsonl", sourcePath)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "16777216")
}
