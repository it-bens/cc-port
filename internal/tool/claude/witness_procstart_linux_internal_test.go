//go:build linux

package claude

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessStart_LinuxRefuses pins that the Linux seam reports no start time
// for a process that certainly exists: /proc/stat btime moves with the wall
// clock, so a start time derived from it would make a live session look
// recycled after a clock step.
func TestProcessStart_LinuxRefuses(t *testing.T) {
	start, err := processStart(os.Getpid())

	require.Error(t, err)
	assert.True(t, start.IsZero(), "a refused read must not report a start time")
}
