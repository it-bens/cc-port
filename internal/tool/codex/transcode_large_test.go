//go:build large

package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReadRolloutLinesRejectsProductionScaleDecompressedStream exercises the
// real production MaxDecompressedBytes cap (not a test-side override) with a
// highly compressible payload, so the on-disk fixture stays tiny even
// though the decoded size is production-scale — the same shape a genuine
// zstd-bomb payload takes.
//
// Tagged `large`: exercised only on demand (see root AGENTS.md); the small-cap
// CI variant in transcode_test.go drives the same branch cheaply.
func TestReadRolloutLinesRejectsProductionScaleDecompressedStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl.zst")
	writeZstdFixture(t, path, []string{strings.Repeat("bomb", 200_000_000)})

	_, _, err := readRolloutLines(path)

	require.Error(t, err)
	_ = os.Remove(path)
}
