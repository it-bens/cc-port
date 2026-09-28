//go:build linux

package claude

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessStart_LinuxReadsOwnProcess pins that the /proc seam returns a real
// start time for a process that certainly exists. Only the upper bound is
// asserted: a wall-clock lower bound would be a different claim (clock skew
// between the kernel's boot-time-derived start and the test's clock), not a
// property of the read.
func TestProcessStart_LinuxReadsOwnProcess(t *testing.T) {
	before := time.Now()

	start, err := processStart(os.Getpid())

	require.NoError(t, err)
	assert.False(t, start.IsZero(), "a live process must have a start time")
	assert.False(t, start.After(before), "a process cannot have started after the probe began")
}

// TestParseStatStarttime_ReadsFieldAfterParenthesisedComm pins the field
// arithmetic against the shape the kernel actually writes: comm is the only
// field that may contain whitespace and parentheses, so the fields are counted
// from after its last closing parenthesis.
func TestParseStatStarttime_ReadsFieldAfterParenthesisedComm(t *testing.T) {
	statLine := "4242 ((ev) (il)) R 1 4242 4242 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 1 0 987654 1234 567 89"

	ticks, err := parseStatStarttime(statLine)

	require.NoError(t, err)
	assert.Equal(t, int64(987654), ticks)
}
