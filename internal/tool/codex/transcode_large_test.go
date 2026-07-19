//go:build large

package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadRolloutLinesRejectsProductionScaleLine exercises the real
// production MaxLineBytes cap with one highly compressible line. The fixture
// is also beyond the whole-stream cap, but Scanner rejects the oversized line
// before readRolloutLines can report the aggregate decompression limit.
//
// Tagged `large`: exercised only on demand (see root AGENTS.md); the small-cap
// test in transcode_test.go asserts the same per-line-over-aggregate precedence.
func TestReadRolloutLinesRejectsProductionScaleLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl.zst")
	writeZstdFixture(t, path, []string{strings.Repeat("bomb", 200_000_000)})

	_, _, err := readRolloutLines(path, DefaultTranscodeCaps())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "token too long")
	_ = os.Remove(path)
}
