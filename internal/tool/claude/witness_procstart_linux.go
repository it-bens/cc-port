//go:build linux

package claude

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// userHZ is the clock-tick rate /proc reports starttime in. The kernel fixes it
// at 100 on every architecture cc-port builds for; it is a compile-time
// constant, not the runtime sysconf(_SC_CLK_TCK) value.
const userHZ = 100

// statStarttimeToken is starttime's position in the token list that resumes
// after the comm field: /proc/<pid>/stat field 3 (state) is token 0, so field
// 22 (starttime) is token 19.
const statStarttimeToken = 19

// processStart returns the start time of the process behind pid, derived from
// the process's starttime in clock ticks since boot plus the kernel's boot
// time. Every read or parse failure is returned; the caller then judges the
// session file by liveness alone.
func processStart(pid int) (time.Time, error) {
	statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return time.Time{}, fmt.Errorf("read start time of pid %d: %w", pid, err)
	}
	ticks, err := parseStatStarttime(string(statData))
	if err != nil {
		return time.Time{}, fmt.Errorf("read start time of pid %d: %w", pid, err)
	}
	boot, err := bootTime()
	if err != nil {
		return time.Time{}, err
	}
	return boot.Add(time.Duration(ticks) * (time.Second / userHZ)), nil
}

// parseStatStarttime extracts the starttime field, in clock ticks since boot,
// from one /proc/<pid>/stat line. The comm field is the only field that may
// contain whitespace or parentheses, so it is skipped by taking everything
// after its last closing parenthesis rather than by splitting from the start.
func parseStatStarttime(statLine string) (int64, error) {
	commEnd := strings.LastIndex(statLine, ")")
	if commEnd < 0 {
		return 0, fmt.Errorf("stat line has no comm field: %q", statLine)
	}
	tokens := strings.Fields(statLine[commEnd+1:])
	if len(tokens) <= statStarttimeToken {
		return 0, fmt.Errorf("stat line has %d fields after comm, need more than %d", len(tokens), statStarttimeToken)
	}
	ticks, err := strconv.ParseInt(tokens[statStarttimeToken], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse starttime field %q: %w", tokens[statStarttimeToken], err)
	}
	return ticks, nil
}

// bootTime returns the kernel's boot time from the btime line of /proc/stat.
func bootTime() (time.Time, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, fmt.Errorf("read /proc/stat: %w", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		value, found := strings.CutPrefix(line, "btime ")
		if !found {
			continue
		}
		seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse /proc/stat btime %q: %w", value, err)
		}
		return time.Unix(seconds, 0), nil
	}
	return time.Time{}, errors.New("read /proc/stat: no btime line")
}
