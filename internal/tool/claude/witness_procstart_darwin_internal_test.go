//go:build darwin

package claude

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessStart_DarwinReadsLiveChildProcess pins that the kernel seam returns
// a real start time for a process that certainly exists. The bound is
// two-sided: the kernel records the start time at fork, which lies between
// beforeSpawn and the read. The lower bound compares against beforeSpawn
// truncated to the second, a margin that covers the resolution gap between the
// kernel's microsecond start time and Go's nanosecond time.Now. The read keeps
// those microseconds; only beforeSpawn is truncated, and only as that margin.
func TestProcessStart_DarwinReadsLiveChildProcess(t *testing.T) {
	beforeSpawn := time.Now()
	child := exec.CommandContext(t.Context(), "sleep", "5")
	require.NoError(t, child.Start())
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	start, err := processStart(child.Process.Pid)

	require.NoError(t, err)
	assert.False(t, start.Before(beforeSpawn.Truncate(time.Second)),
		"the kernel records the start at fork, after beforeSpawn truncated to the second")
	assert.False(t, start.After(time.Now()), "a process cannot have started after the read")
}

// TestProcessStart_DarwinRefusesReapedChild pins that a pid the kernel no longer
// tracks yields an error rather than a zero time, which is what lets the caller
// fall back to liveness alone.
func TestProcessStart_DarwinRefusesReapedChild(t *testing.T) {
	child := exec.CommandContext(t.Context(), "sleep", "5")
	require.NoError(t, child.Start())
	require.NoError(t, child.Process.Kill())
	// Wait reaps the child so the kernel drops its record; the kill makes it
	// exit non-zero, so Wait's error is expected rather than a failure.
	_ = child.Wait()

	start, err := processStart(child.Process.Pid)

	require.Error(t, err)
	assert.True(t, start.IsZero(), "a refused read must not report a start time")
}
