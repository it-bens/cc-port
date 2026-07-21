# internal/lock: agent notes

## Before editing

- Use `Acquire` when a caller must hold several tools' locks at once through
  a multi-target apply; use `WithLock` when one call needs the lock only for
  its own duration (README §Concurrency guard).
- Run the witness before acquiring the flock, for both entry points (README
  §Concurrency guard).
- Keep read-only operations outside both `Acquire` and `WithLock` (README
  §Concurrency guard).
- Keep the deferred release path (README §Public API).

## Navigation

- Entry points: `lock.go` (`Acquire`, `WithLock`).
- Live-session witness: `internal/tool/claude/witness.go` (`FindActive`).
- Test hook: `lock.go` (`unlockFn`).
- Tests: `lock_test.go` (in-package).
