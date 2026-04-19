# internal/lock

## Purpose

Acquire an exclusive advisory lock on `~/.claude/.cc-port.lock` and refuse any mutation while another cc-port invocation holds it or a live Claude Code session is running on the host.

Not a general mutex — the lock scope is the whole `~/.claude` tree, and the live-session check is specific to Claude Code's `sessions/*.json` format. Any future per-file locking should not reuse this package.

## Public API

- `WithLock(claudeHome *claude.Home, fn func() error) error` — the sole lock-guarded entry point. Runs the live-session check, takes `~/.claude/.cc-port.lock` via `gofrs/flock`, calls `fn` with the lock held, and releases the lock via `defer` on every exit path — including a panic inside `fn` that a caller recovers. Returns `fn`'s error when non-nil; on the success path, any release error surfaces wrapped as `release cc-port lock: %w`. Acquire errors (live-session abort, contention) are returned without invoking `fn`.
- `LockFileName` — constant; the name (`.cc-port.lock`) of the advisory-lock file cc-port creates inside the Claude Code home directory.

## Contracts

### Concurrency guard

Before mutating shared files under `~/.claude/`, mutating commands wrap
their work in `lock.WithLock`, which acquires an exclusive advisory lock
on `~/.claude/.cc-port.lock` and scans `~/.claude/sessions/*.json` for
entries whose recorded `pid` is alive on the host. If either check finds
something, the invocation aborts before any files are touched. The
kernel releases the lock when cc-port exits, so a crash does not leave a
stale block on the next invocation.

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

- `internal/move/README.md` §Apply contract — `move.Apply` wraps its body in `lock.WithLock`.
- `internal/importer/README.md` §Import contract — `importer.Run` wraps its body in `lock.WithLock`.

Handled — invariants this package enforces on every call:

- The lock is released via `defer` on every exit path from `WithLock`, including success, returned error, and panic. A caller that uses `recover()` can safely call `WithLock` again in the same process; the flock has already been dropped by the time the second call runs.
- The kernel releases the flock when the cc-port process exits, so a crash between `TryLock` and `defer Unlock` does not leave a stale block on the next invocation.

Refused — callers cannot:

- Skip the live-session check. `WithLock` runs it before taking the lock; there is no `WithLockNoLiveCheck` alternative.
- Take the lock without invoking `fn`. Every code path that reaches `TryLock` follows through to `fn` and the deferred release.

Not covered — out-of-scope concerns:

- Windows support. The `//go:build darwin || linux` tag stays on `lock.go`; `gofrs/flock` itself is cross-platform, but the rest of cc-port (path encoding, session enumeration) is Unix-shaped.
- Cross-host locking (NFS, AFS). `flock(2)` semantics on networked filesystems are implementation-defined; cc-port assumes a local filesystem.

## Quirks

- `Flock.Unlock` does not delete the lock file; cc-port also does not. The file stub `~/.claude/.cc-port.lock` remains on disk between invocations (`go doc github.com/gofrs/flock.Flock.Unlock` documents this behavior). Do not add `os.Remove(lockPath)` — it would introduce a race where a sibling invocation is mid-`OpenFile` while we remove the entry, and there is no user-visible benefit to cleaning it up.

## Tests

Unit tests in `lock_test.go` (`package lock` — same-package access to the unexported `findActiveSessions`/`processAlive` and to the `unlockFn` swap point). Coverage: acquire with no live sessions, acquire when a session PID is dead, abort when a session PID is alive, abort when another cc-port process holds the lock, acquire after a previous release, WithLock calls fn with the lock held, WithLock releases on fn success, WithLock releases on fn error, WithLock propagates acquire errors, WithLock releases the lock after a panic in fn is recovered, WithLock surfaces release errors on the fn-success path and suppresses them on the fn-error path.

## References

- `github.com/gofrs/flock` — local authoritative: `go doc github.com/gofrs/flock.Flock` (lists every method on `*Flock` at the exact version pinned in `go.mod`) · online supplement: https://pkg.go.dev/github.com/gofrs/flock (release metadata, CVEs).
- `flock(2)` — the BSD advisory-lock system call gofrs/flock wraps. Release at process exit is a kernel guarantee regardless of how the program terminates.
