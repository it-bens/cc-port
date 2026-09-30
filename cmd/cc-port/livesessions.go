package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/tool"
)

const (
	ignoreLiveSessionsFlag  = "ignore-live-sessions"
	ignoreLiveSessionsUsage = "proceed while Claude Code or Codex sessions are running (see --help for what can be lost)"

	moveLiveSessionsHelp = "--ignore-live-sessions proceeds while Claude Code or Codex sessions are running. " +
		"A session on the moved project loses writes: its files are rewritten or renamed under it. " +
		"Any running session can collide with cc-port on a shared history or config file within a window " +
		"of milliseconds; either the session's write is lost, or the session's own read-modify-write " +
		"replaces cc-port's rewrite of that file. While Codex holds a database, cc-port waits up to " +
		"5 seconds for each database and then fails the Codex part of the move. The Claude part stays " +
		"applied. Re-running the move once the sessions on the moved project are closed and Codex is idle " +
		"completes it. Every Codex session, on any project, uses the same databases. While cc-port " +
		"rewrites Codex's databases, it holds their write lock. A Codex write waits up to its own " +
		"5 seconds and then fails, and a Codex transaction that reads before it writes fails at once."

	importAndPullLiveSessionsHelp = "--ignore-live-sessions proceeds while Claude Code or Codex sessions are running. " +
		"A session on the target project can have its files replaced by the archive's. " +
		"Any running session can collide with cc-port on a shared history or config file within a window " +
		"of milliseconds; either the session's write is lost, or the session's own read-modify-write " +
		"replaces cc-port's rewrite of that file. While cc-port rewrites Codex's databases, it holds " +
		"their write lock. A Codex write waits up to its own 5 seconds and then fails, and a Codex " +
		"transaction that reads before it writes fails at once. A Codex database still busy after " +
		"cc-port's 5-second wait fails the import after its files are promoted. Re-running the import " +
		"completes it."
)

// newIgnoredWriters returns the invocation's collector: a ready collector
// with --ignore-live-sessions, nil (the refusing mode) without it, and a
// usage error when the flag comes without --apply.
func newIgnoredWriters(ignoreLiveSessions, apply bool) (*lock.IgnoredWriters, error) {
	if !ignoreLiveSessions {
		return nil, nil
	}
	if !apply {
		return nil, &usageError{err: errors.New("--ignore-live-sessions requires --apply")}
	}
	return &lock.IgnoredWriters{}, nil
}

// renderIgnoredWriters writes one warning line per ignored writer to
// stderr. A nil ignored writes nothing.
func renderIgnoredWriters(stderr io.Writer, targets []tool.Target, ignored *lock.IgnoredWriters) error {
	if ignored == nil {
		return nil
	}
	for _, entry := range ignored.List() {
		_, err := fmt.Fprintf(
			stderr, "Warning: ignored live %s writer: %s\n", displayName(targets, entry.Tool), entry.Writer.String(),
		)
		if err != nil {
			return fmt.Errorf("write ignored writer: %w", err)
		}
	}
	return nil
}

// withLiveSessionsHint appends the --ignore-live-sessions hint to err when
// a live writer refused the command.
func withLiveSessionsHint(err error) error {
	if _, ok := errors.AsType[*lock.LiveSessionsError](err); !ok {
		return err
	}
	return fmt.Errorf("%w; pass --ignore-live-sessions to proceed anyway", err)
}

// endApplyPath ends the apply path of move, import and pull: it renders the
// ignored live writers to stderr and returns runErr carrying the hint, joined
// with the render error. Callers print the rest of their output after it and
// join their own write errors onto its result, so a failed render never drops
// a result, note or warning that would otherwise have been printed.
func endApplyPath(stderr io.Writer, targets []tool.Target, ignored *lock.IgnoredWriters, runErr error) error {
	renderErr := renderIgnoredWriters(stderr, targets, ignored)
	return errors.Join(withLiveSessionsHint(runErr), renderErr)
}
