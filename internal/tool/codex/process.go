//go:build darwin || linux

package codex

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ProcessInfo is one running process observed by a ProcessLister: its pid
// and short command name, matching what `ps -o pid=,comm=` reports.
type ProcessInfo struct {
	PID  int
	Name string
}

// ProcessLister enumerates every process currently running on the machine.
// Adapter's default is listSystemProcesses; tests inject a fake so the
// witness's process-table evidence is driven without touching the live
// process table (spec §1 construction seams).
type ProcessLister func() ([]ProcessInfo, error)

// listSystemProcesses is the default ProcessLister. It shells out to `ps`,
// present on both darwin and linux, rather than parsing /proc or binding a
// per-OS process-enumeration API for a simple pid+command-name listing.
func listSystemProcesses() ([]ProcessInfo, error) {
	output, err := exec.CommandContext(context.Background(), "ps", "-Ao", "pid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("run ps: %w", err)
	}
	return parsePSOutput(output)
}

// parsePSOutput parses `ps -Ao pid=,comm=` output. comm is taken as
// filepath.Base by the caller, since some ps implementations (notably
// macOS) report the full executable path rather than a short name.
func parsePSOutput(output []byte) ([]ProcessInfo, error) {
	var processes []ProcessInfo
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		processes = append(processes, ProcessInfo{PID: pid, Name: fields[1]})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan process list: %w", err)
	}
	return processes, nil
}
