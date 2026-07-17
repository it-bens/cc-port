# internal/lock

## Purpose

Acquires an exclusive advisory lock at a caller-provided path. A caller-provided
witness blocks mutation while a live writer is present.

## Public API

- `WithLock(lockPath string, witness func() ([]tool.ActiveWriter, error), fn func() error) error`:
  the sole lock-guarded entry point. Runs `witness` first, then acquires
  `lockPath` via `gofrs/flock`, and calls `fn` with the lock held. `defer`
  releases the lock on every exit path. It also runs after a panic in `fn`
  that a caller recovers. Error
  precedence:
  1. Witness finds a live writer: returns a
     descriptive error, does not take the lock, and does not invoke `fn`.
  2. Another cc-port invocation holds the lock: returns a contention error
     and does not invoke `fn`.
  3. `fn` returns a non-nil error: that error is returned. The deferred release
     still runs, and its error (if any) is dropped because the caller's
     operational error takes precedence.
  4. `fn` returns nil: a deferred unlock error surfaces wrapped as
     `release cc-port lock: %w`; if unlock succeeds but removing the lock
     file fails, the error surfaces as `remove cc-port lock file: %w`.
- `tool.ActiveWriter`: witness result with `Pid int` and `Cwd string`.
  `internal/tool/claude.FindActive` supplies Claude Code evidence.
- `FileName`: constant. The name (`.cc-port.lock`) of the advisory-lock file
  cc-port creates inside the Claude Code home directory.

### Errors

- `LiveSessionsError`: typed error returned by `WithLock` when the witness
  finds one or more live writers before the lock is taken. `Sessions` carries
  the witness list as `[]tool.ActiveWriter`; tests assert via
  `errors.As`. `WithLock` takes the lock only when the list is empty. Reachable
  from `move.Apply`, which returns it unchanged from `lock.WithLock`.
- `ErrConcurrentInvocation`: returned by `WithLock` when another cc-port
  invocation already holds the advisory lock. The wrapping message names the
  contended home directory; tests assert via `errors.Is`.
- `ErrUnlockFailure`: returned by `WithLock` when releasing the lock fails on the
  `fn`-success path. Joins the underlying unlock cause via `%w`, so `errors.Is`
  matches both this sentinel and the cause.

## Contracts

### Concurrency guard

Before mutating shared files, `importer.Run` nests `lock.WithLock` for every
selected tool in registry order. `move.Apply` instead preflights each selected
target's `MoveSurfaces`, then calls `lock.Acquire`; no target applies until every
witness and flock succeeds. Move holds all acquired locks through the full apply
and releases them in reverse order.

`Acquire` runs the witness before flock acquisition. `WithLock` is the
single-lock convenience wrapper around `Acquire` and deferred `Held.Release`.
Any witness result blocks the invocation before it writes files.

The kernel releases the lock when cc-port exits, so a crash does not leave a
stale block on the next invocation.

Guarded commands (these take the lock and run the live-session check):

- `cc-port move --apply` (direct `Acquire` across selected tools)
- `cc-port import` (nested `WithLock` across selected tools)

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

- `internal/move/README.md §Apply contract` (`move.Apply` preflights and retains
  one `lock.Acquire` result per selected tool).
- `internal/importer/README.md §Import contract` (`importer.Run` nests
  `lock.WithLock` across selected tools).

#### Handled

- The lock is released via `defer` on every exit path from `WithLock`,
  including success, returned error, and panic. A caller that uses `recover()`
  can safely call `WithLock` again in the same process. The flock has already
  been dropped by the time the second call runs.
- The kernel releases the flock when cc-port exits. A crash between `TryLock`
  and the deferred `Unlock` does not leave a stale block on the next
  invocation.

#### Refused

- Skip the witness. `WithLock` requires a non-nil witness before taking the lock.
- Take the lock without invoking `fn`. Every code path that reaches `TryLock`
  follows through to `fn` and the deferred release.

#### Not covered

- Windows support. The `//go:build darwin || linux` tag stays on `lock.go`.
  `gofrs/flock` itself is cross-platform, but the rest of cc-port (path
  encoding, session enumeration) is Unix-shaped.
- Cross-host locking (NFS, AFS). `flock(2)` semantics on networked filesystems
  are implementation-defined. cc-port assumes a local filesystem.
- Three-process race at cleanup time. cc-port removes the lock file after
  `Unlock` (see §Quirks). If invocation A unlocks and unlinks while
  invocation B is between `OpenFile(O_CREATE)` and `flock()` on the same
  path, B's flock lands on A's now-unlinked inode. A third invocation C
  arriving after the unlink does `OpenFile(O_CREATE)` and creates a fresh
  inode; its flock lands on that fresh inode. B and C both hold exclusive
  flocks on different inodes and no longer exclude each other. The window
  is one `flock(2)` call wide and requires three concurrent cc-port
  invocations, so interactive use cannot reach it; scripted parallel runs
  against the same `~/.claude/` can.

## Quirks

`Flock.Unlock` does not delete the lock file (`go doc
github.com/gofrs/flock.Flock.Unlock` documents this behavior). cc-port calls
`os.Remove(lockPath)` in the same defer right after `Unlock`, so
`~/.claude/` does not accumulate a `.cc-port.lock` stub after a normal run.
Removal is unconditional: unlink is orthogonal to flock state, and skipping
it on an unlock error would just leak stubs.

The cleanup exposes a narrow three-process race. See §Concurrency guard
§Not covered.

## Tests

Unit tests in `lock_test.go` use the `unlockFn` swap point to simulate release
failure:

- Acquire with no live sessions.
- Acquire when a session PID is dead.
- Abort when a witness reports a live writer.
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
