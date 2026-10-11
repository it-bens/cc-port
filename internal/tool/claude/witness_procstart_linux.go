//go:build linux

package claude

import (
	"fmt"
	"time"
)

// ProcessStart reports that Linux has no stable wall-clock process start time:
// /proc/stat btime is recomputed from the wall clock on every read (getboottime64).
func ProcessStart(pid int) (time.Time, error) {
	return time.Time{}, fmt.Errorf("process start time of pid %d: linux has no stable wall-clock process start time", pid)
}
