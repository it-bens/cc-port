package codex

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/it-bens/cc-port/internal/tool"
)

// Rollout root subdirectories (rollout/src/lib.rs:21-22).
const (
	sessionsSubdir         = "sessions"
	archivedSessionsSubdir = "archived_sessions"
)

// codexProcessNames are the binary names a running Codex install can
// appear under in the process table, per the workspace Cargo.toml
// `[[bin]]` declarations for the CLI/TUI (cli/Cargo.toml, tui/Cargo.toml)
// and the app-server the daemon manages (app-server/Cargo.toml).
var codexProcessNames = map[string]bool{
	"codex":            true,
	"codex-tui":        true,
	"codex-app-server": true,
}

// ActiveWriters implements tool.Workspace. It gathers liveness evidence in
// the order documented at spec §6.4. Every source runs regardless of an
// earlier source's outcome, so a dry-run can report every signal at once;
// but a source that cannot be read makes the whole call fail, wrapping
// tool.ErrNoWitness, which blocks mutation exactly like a positive result.
func (workspace *Workspace) ActiveWriters() ([]tool.ActiveWriter, error) {
	var active []tool.ActiveWriter
	var readErrs []error

	collect := func(writers []tool.ActiveWriter, err error) {
		if err != nil {
			readErrs = append(readErrs, err)
			return
		}
		active = append(active, writers...)
	}

	collect(workspace.processTableWitness())
	collect(workspace.busyProbeWitness())

	if len(readErrs) > 0 {
		return nil, fmt.Errorf("%w: %w", tool.ErrNoWitness, errors.Join(readErrs...))
	}
	return active, nil
}

// processTableWitness is evidence source 1: a running codex process is the
// primary signal, since a plain `codex`/`codex exec` run holds a database
// open with no daemon directory and no marker file.
func (workspace *Workspace) processTableWitness() ([]tool.ActiveWriter, error) {
	processes, err := workspace.listProcesses()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	var active []tool.ActiveWriter
	for _, process := range processes {
		if codexProcessNames[filepath.Base(process.Name)] {
			active = append(active, tool.ActiveWriter{Pid: process.PID})
		}
	}
	return active, nil
}

// busyProbeWitness is evidence source 2: SQLITE_BUSY on a BEGIN IMMEDIATE
// probe against each discovered database, backstopping the process table for
// a writer this witness otherwise cannot see.
func (workspace *Workspace) busyProbeWitness() ([]tool.ActiveWriter, error) {
	databases, err := workspace.allDatabasePaths()
	if err != nil {
		return nil, err
	}
	var active []tool.ActiveWriter
	for _, database := range databases {
		busy, probeErr := probeDatabaseBusy(database)
		if probeErr != nil {
			return nil, probeErr
		}
		if busy {
			active = append(active, tool.ActiveWriter{})
		}
	}
	return active, nil
}
