//go:build darwin

package claude

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessStart_DarwinReadsOwnProcess pins that the kernel seam returns a
// real start time for a process that certainly exists. Only the upper bound is
// asserted: a wall-clock lower bound would be a different claim (clock skew
// between the kernel's start time and the test's clock), not a property of the
// read.
func TestProcessStart_DarwinReadsOwnProcess(t *testing.T) {
	before := time.Now()

	start, err := processStart(os.Getpid())

	require.NoError(t, err)
	assert.False(t, start.IsZero(), "a live process must have a start time")
	assert.False(t, start.After(before), "a process cannot have started after the probe began")
}
