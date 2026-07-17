package codex

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/it-bens/cc-port/internal/rewrite"
)

// zstSuffix marks a compressed rollout sibling (rollout/src/compression.rs:18,
// COMPRESSED_SUFFIX).
const zstSuffix = ".zst"

// zstdCompressionLevel matches Codex's own cold-rollout compression level
// (rollout/src/compression.rs:252, COMPRESSION_LEVEL).
const zstdCompressionLevel = 3

// TranscodeCaps bounds zstd decompression. MaxDecompressedBytes is a
// whole-stream cap (stops a bomb that expands into an unbounded total,
// whether as one giant line or many small ones); MaxLineBytes is a
// per-line bufio.Scanner cap (stops a bomb built as one absurdly long line
// even within total budget). They guard different bomb shapes and are
// both mandatory.
type TranscodeCaps struct {
	MaxDecompressedBytes int64
	MaxLineBytes         int
}

var defaultTranscodeCaps = TranscodeCaps{MaxDecompressedBytes: 512 << 20, MaxLineBytes: 16 << 20}

// activeTranscodeCaps is the cap set every read in this package consults.
// Package state, not a parameter, mirroring internal/archive's Caps/SetCaps
// pairing pattern for the same reason: the caps are a process-wide safety
// backstop, and SetTranscodeCaps is the sanctioned override for tests that
// need to exercise rejection without materializing production-scale
// fixtures.
var activeTranscodeCaps = defaultTranscodeCaps

// SetTranscodeCaps overrides the active zstd decompression caps and
// returns a restore function. Exported for tests exercising cap rejection
// cheaply; production code never calls it.
func SetTranscodeCaps(caps TranscodeCaps) (restore func()) {
	previous := activeTranscodeCaps
	activeTranscodeCaps = caps
	return func() { activeTranscodeCaps = previous }
}

// countingReader tallies the bytes actually produced by inner, so a caller
// reading through an io.LimitReader wrapping it can tell whether the real
// stream was longer than the limit (the limiter alone just stops asking).
type countingReader struct {
	inner io.Reader
	read  int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.inner.Read(p)
	c.read += int64(n)
	return n, err
}

// readRolloutLines reads path, transparently decompressing a .jsonl.zst
// sibling, and returns every line with its terminator stripped. Both
// mandatory decompression bounds apply: a whole-stream io.LimitReader cap
// and a per-line bufio.Scanner cap.
func readRolloutLines(path string) (lines [][]byte, compressed bool, err error) {
	file, err := os.Open(path) //nolint:gosec // G304: path from adapter-controlled rollout discovery
	if err != nil {
		return nil, false, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	compressed = strings.HasSuffix(path, zstSuffix)
	var reader io.Reader = file
	var counter *countingReader
	if compressed {
		decoder, decErr := zstd.NewReader(file)
		if decErr != nil {
			return nil, false, fmt.Errorf("open zstd decoder for %s: %w", path, decErr)
		}
		defer decoder.Close()
		counter = &countingReader{inner: decoder}
		reader = io.LimitReader(counter, activeTranscodeCaps.MaxDecompressedBytes+1)
	}

	// bufio.Scanner.Buffer's effective cap is the larger of max and
	// cap(initialBuf); a fixed 64 KiB initial buffer would silently widen
	// a smaller test-side MaxLineBytes override back up to 64 KiB.
	initialBufSize := 64 << 10
	if activeTranscodeCaps.MaxLineBytes < initialBufSize {
		initialBufSize = activeTranscodeCaps.MaxLineBytes
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, initialBufSize), activeTranscodeCaps.MaxLineBytes)
	for scanner.Scan() {
		lines = append(lines, append([]byte(nil), scanner.Bytes()...))
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, compressed, fmt.Errorf("scan %s: %w", path, scanErr)
	}
	if counter != nil && counter.read > activeTranscodeCaps.MaxDecompressedBytes {
		return nil, compressed, fmt.Errorf(
			"%s: decompressed size exceeds %d bytes", path, activeTranscodeCaps.MaxDecompressedBytes,
		)
	}
	return lines, compressed, nil
}

// TranscodeLines rewrites path (a rollout .jsonl file or its .jsonl.zst
// compressed sibling) line by line through transform: it decompresses when
// the name ends in .zst, applies transform to each line, recompresses at
// level 3 when the source was compressed, and promotes the result through
// rewrite.SafeWriteFile so the rewrite is atomic. transform returns how
// many bounded occurrences it changed in that line (not just whether the
// line changed), so the total matches a Plan pass that counts occurrences
// rather than lines.
func TranscodeLines(path string, transform func(line []byte) (rewritten []byte, count int)) (int, error) {
	lines, compressed, err := readRolloutLines(path)
	if err != nil {
		return 0, err
	}

	var output bytes.Buffer
	count := 0
	for _, line := range lines {
		rewrittenLine, lineCount := transform(line)
		count += lineCount
		output.Write(rewrittenLine)
		output.WriteByte('\n')
	}

	finalBytes := output.Bytes()
	if compressed {
		finalBytes, err = compressZstd(finalBytes)
		if err != nil {
			return 0, fmt.Errorf("compress %s: %w", path, err)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := rewrite.SafeWriteFile(path, finalBytes, info.Mode()); err != nil {
		return 0, fmt.Errorf("write %s: %w", path, err)
	}
	return count, nil
}

func compressZstd(data []byte) ([]byte, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(zstdCompressionLevel)))
	if err != nil {
		return nil, fmt.Errorf("create zstd encoder: %w", err)
	}
	defer func() { _ = encoder.Close() }()
	return encoder.EncodeAll(data, nil), nil
}
