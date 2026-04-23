# internal/lock

## Purpose

Acquires an exclusive advisory lock on `~/.claude/.cc-port.lock`. Refuses
any mutation while another cc-port invocation holds it or a live Claude Code
session is running on the host.

The lock scope covers the whole `~/.claude` tree and the live-session check
reads Claude Code's `sessions/*.json` format. Any future per-file locking
should not reuse this package.

## Public API

- `WithLock(claudeHome *claude.Home, fn func() error) error`: the sole
  lock-guarded entry point. Runs the live-session check (via `FindActive`)
  first, then acquires `~/.claude/.cc-port.lock` via `gofrs/flock`, and
  calls `fn` with the lock held. The lock is released via `defer` on every
  exit path, including a panic inside `fn` that a caller recovers. Error
  precedence:
  1. Live-session check finds a running Claude Code process: returns a
     descriptive error, does not take the lock, and does not invoke `fn`.
  2. Another cc-port invocation holds the lock: returns a contention error
     and does not invoke `fn`.
  3. `fn` returns a non-nil error: that error is returned. The deferred release
     still runs, and its error (if any) is dropped because the caller's
     operational error takes precedence.
  4. `fn` returns nil: any deferred release error surfaces wrapped as
     `release cc-port lock: %w`.
- `FindActive(claudeHome *claude.Home) ([]ActiveSession, error)`: returns
  one `ActiveSession{Pid, Cwd}` per `~/.claude/sessions/*.json` file whose
  recorded PID is alive on the host. Missing or empty `sessions/` yields a
  nil slice and no error. Read-only callers (dry-run heads-up, inspection
  tooling) use this without taking the flock; `WithLock` uses it
  internally and refuses on any non-empty result.
- `ActiveSession`: struct with `Pid int` and `Cwd string`. One instance per
  live Claude Code process detected.
- `FileName`: constant. The name (`.cc-port.lock`) of the advisory-lock file
  cc-port creates inside the Claude Code home directory.

## Contracts

### Concurrency guard

Before mutating shared files under `~/.claude/`, mutating commands wrap their
work in `lock.WithLock`. It acquires an exclusive advisory lock on
`~/.claude/.cc-port.lock`. It also scans `~/.claude/sessions/*.json` for
entries whose recorded `pid` is alive on the host. If either check finds
something, the invocation aborts before any files are touched.

The kernel releases the lock when cc-port exits, so a crash does not leave a
stale block on the next invocation.

Guarded commands (these take the lock and run the live-session check):

- `cc-port move --apply`
- `cc-port import`

Not guarded (these are read-only with respect to `~/.claude/` and run without
locking or session detection):

- `cc-port move` (dry-run): counts potential replacements without writing.
  A concurrent Claude Code write can skew the reported counts but cannot
  corrupt data.
- `cc-port export` and `cc-port export manifest`: read from `~/.claude/` and
  write only to the output archive or manifest file outside it. A concurrent
  Claude Code write during a long export can produce an internally inconsistent
  archive, but nothing under `~/.claude/` changes.

Called by:

- `internal/move/README.md §Apply contract` (`move.Apply` wraps its body in
  `lock.WithLock`).
- `internal/importer/README.md §Import contract` (`importer.Run` wraps its
  body in `lock.WithLock`).

#### Handled

- The lock is released via `defer` on every exit path from `WithLock`,
  including success, returned error, and panic. A caller that uses `recover()`
  can safely call `WithLock` again in the same process. The flock has already
  been dropped by the time the second call runs.
- The kernel releases the flock when cc-port exits. A crash between `TryLock`
  and the deferred `Unlock` does not leave a stale block on the next
  invocation.

#### Refused

- Skip the live-session check. `WithLock` runs it before taking the lock.
  There is no `WithLockNoLiveCheck` alternative.
- Take the lock without invoking `fn`. Every code path that reaches `TryLock`
  follows through to `fn` and the deferred release.

#### Not covered

- Windows support. The `//go:build darwin || linux` tag stays on `lock.go`.
  `gofrs/flock` itself is cross-platform, but the rest of cc-port (path
  encoding, session enumeration) is Unix-shaped.
- Cross-host locking (NFS, AFS). `flock(2)` semantics on networked filesystems
  are implementation-defined. cc-port assumes a local filesystem.

## Quirks

`Flock.Unlock` does not delete the lock file. cc-port also does not. The file
stub `~/.claude/.cc-port.lock` remains on disk between invocations (`go doc
github.com/gofrs/flock.Flock.Unlock` documents this behavior).

Do not add `os.Remove(lockPath)`. That would introduce a race where a sibling
invocation is mid-`OpenFile` while we remove the entry. There is no
user-visible benefit to cleaning it up.

## Tests

Unit tests in `lock_test.go` (`package lock`, same-package access to the
unexported `processAlive` and the `unlockFn` swap point; `FindActive` is
exported and exercised directly):

- Acquire with no live sessions.
- Acquire when a session PID is dead.
- Abort when a session PID is alive.
- Abort when another cc-port process holds the lock.
- Acquire after a previous release.
- `WithLock` calls `fn` with the lock held.
- `WithLock` releases on `fn` success.
- `WithLock` releases on `fn` error.
- `WithLock` propagates acquire errors.
- `WithLock` releases the lock after a panic in `fn` is recovered.
- `WithLock` surfaces release errors on the `fn`-success path and suppresses
  them on the `fn`-error path.

## References

- `github.com/gofrs/flock`: local authoritative: `go doc github.com/gofrs/flock.Flock`
  (lists every method on `*Flock` at the exact version pinned in `go.mod`).
  Online supplement: https://pkg.go.dev/github.com/gofrs/flock (release
  metadata, CVEs).
- `flock(2)`: the BSD advisory-lock system call gofrs/flock wraps. Release at
  process exit is a kernel guarantee regardless of how the program terminates.
