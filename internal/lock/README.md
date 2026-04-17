# internal/lock

## Purpose

Acquire an exclusive advisory lock on `~/.claude/.cc-port.lock` and refuse any mutation while another cc-port invocation holds it or a live Claude Code session is running on the host.

Not a general mutex — the lock scope is the whole `~/.claude` tree, and the live-session check is specific to Claude Code's `sessions/*.json` format. Any future per-file locking should not reuse this package.

## Public API

- `Acquire(claudeHome *claude.Home) (*Lock, error)` — acquires the lock and runs the live-session check; returns a handle whose `Release` must be deferred by the caller.
- `Lock` — type; exposes `Release() error`.
- `LockFileName` — constant; the name (`.cc-port.lock`) of the advisory-lock file cc-port creates inside the Claude Code home directory.

## Contracts

### Concurrency guard

Before mutating shared files under `~/.claude/`, cc-port acquires an
exclusive advisory lock on `~/.claude/.cc-port.lock` and scans
`~/.claude/sessions/*.json` for entries whose recorded `pid` is alive on
the host. If either check finds something, the invocation aborts before
any files are touched. The kernel releases the lock when cc-port exits,
so a crash does not leave a stale block on the next invocation.

Guarded commands — these take the lock and run the live-session check:

- `cc-port move --apply`
- `cc-port import`

Not guarded — these are read-only with respect to `~/.claude/` and run
without locking or session detection:

- `cc-port move` (dry-run) — counts potential replacements without
  writing. A concurrent Claude Code write can skew the reported counts
  but cannot corrupt data.
- `cc-port export` and `cc-port export manifest` — read from
  `~/.claude/` and write only to the output archive or manifest file
  outside it. A concurrent Claude Code write during a long export can
  produce an internally inconsistent archive (e.g. a history snapshot
  that does not line up with a transcript snapshot), but nothing under
  `~/.claude/` changes.

Called by:

- `internal/move/README.md` §Malformed history entries preserved — both `Apply` paths acquire the lock before any write.
- `internal/importer/README.md` §Import contract — `importer.Run` acquires the lock before reading the archive.

## Tests

Unit tests in `lock_test.go`. Coverage: acquire with no live sessions, acquire when a session PID is dead, abort when a session PID is alive, abort when another cc-port process holds the lock, acquire after a previous release, release idempotency.
