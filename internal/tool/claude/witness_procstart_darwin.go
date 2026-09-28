//go:build darwin

package claude

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// processStart returns the start time of the process behind pid, read from the
// kernel's kinfo_proc record for that pid. A pid with no live process has no
// record, so the sysctl fails instead of reporting a zero time; the caller then
// judges the session file by liveness alone.
func processStart(pid int) (time.Time, error) {
	kinfo, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, fmt.Errorf("read start time of pid %d: %w", pid, err)
	}
	started := kinfo.Proc.P_starttime
	return time.Unix(started.Sec, int64(started.Usec)*1000), nil
}
