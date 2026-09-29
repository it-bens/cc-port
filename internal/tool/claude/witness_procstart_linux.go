//go:build linux

package claude

import (
	"fmt"
	"time"
)

// processStart reports that Linux has no stable wall-clock process start time:
// /proc/stat btime is recomputed from the wall clock on every read (getboottime64).
func processStart(pid int) (time.Time, error) {
	return time.Time{}, fmt.Errorf("process start time of pid %d: linux has no stable wall-clock process start time", pid)
}
