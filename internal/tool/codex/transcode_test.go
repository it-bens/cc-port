package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeZstdFixture(t *testing.T, path string, lines []string) {
	t.Helper()
	plain := strings.Join(lines, "\n") + "\n"
	compressed, err := compressZstd([]byte(plain))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, compressed, 0o600))
}

func TestTranscodeLinesRoundTripsPlainFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("line one\nline two\n"), 0o600))

	changed, err := TranscodeLines(path, func(line []byte) ([]byte, int) {
		if bytes.Equal(line, []byte("line one")) {
			return []byte("LINE ONE"), 1
		}
		return line, 0
	})

	require.NoError(t, err)
	assert.Equal(t, 1, changed)
	data, err := os.ReadFile(path) //nolint:gosec // G304: path built from t.TempDir() in this test
	require.NoError(t, err)
	assert.Equal(t, "LINE ONE\nline two\n", string(data))
}

func TestTranscodeLinesRoundTripsCompressedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl.zst")
	writeZstdFixture(t, path, []string{"line one", "line two"})

	changed, err := TranscodeLines(path, func(line []byte) ([]byte, int) {
		if bytes.Equal(line, []byte("line one")) {
			return []byte("LINE ONE"), 1
		}
		return line, 0
	})

	require.NoError(t, err)
	assert.Equal(t, 1, changed)

	decoded, _, decoderErr := readRolloutLines(path)
	require.NoError(t, decoderErr)
	var got []string
	for _, line := range decoded {
		got = append(got, string(line))
	}
	assert.Equal(t, []string{"LINE ONE", "line two"}, got)

	// Confirm the file is still a real zstd frame, not silently left plain.
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path built from t.TempDir() in this test
	require.NoError(t, err)
	decoder, err := zstd.NewReader(nil)
	require.NoError(t, err)
	defer decoder.Close()
	_, err = decoder.DecodeAll(raw, nil)
	require.NoError(t, err, "output must remain a valid zstd frame")
}

func TestTranscodeLinesRejectsOversizedLine(t *testing.T) {
	restore := SetTranscodeCaps(TranscodeCaps{MaxDecompressedBytes: 1 << 20, MaxLineBytes: 16})
	defer restore()

	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", 64)+"\n"), 0o600))

	_, _, err := readRolloutLines(path)

	require.Error(t, err)
}

func TestTranscodeLinesRejectsOversizedDecompressedStream(t *testing.T) {
	restore := SetTranscodeCaps(TranscodeCaps{MaxDecompressedBytes: 32, MaxLineBytes: 16 << 20})
	defer restore()

	path := filepath.Join(t.TempDir(), "rollout.jsonl.zst")
	writeZstdFixture(t, path, []string{strings.Repeat("a", 100)})

	_, _, err := readRolloutLines(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}
