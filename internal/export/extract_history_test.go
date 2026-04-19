package export

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractProjectHistory_AcceptsLargeLine(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-line test in short mode")
	}
	tempDir := t.TempDir()
	historyPath := filepath.Join(tempDir, "history.jsonl")

	// 1 MiB history line that mentions the project path.
	largeLine := bytes.Repeat([]byte("a"), 1<<20)
	line := append(
		[]byte(`{"cwd":"/Users/test/proj","display":"`),
		append(largeLine, []byte(`"}`)...)...,
	)
	line = append(line, '\n')
	require.NoError(t, os.WriteFile(historyPath, line, 0600))

	result, err := extractProjectHistory(historyPath, "/Users/test/proj")
	require.NoError(t, err)
	assert.NotEmpty(t, result)
}

func TestExtractProjectHistory_RejectsLineOverLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping over-limit-line test in short mode")
	}
	tempDir := t.TempDir()
	historyPath := filepath.Join(tempDir, "history.jsonl")

	huge := bytes.Repeat([]byte("a"), 17<<20)
	require.NoError(t, os.WriteFile(historyPath, huge, 0600))

	_, err := extractProjectHistory(historyPath, "/Users/test/proj")
	require.Error(t, err)
	assert.ErrorIs(t, err, bufio.ErrTooLong)
}
