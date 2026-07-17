# internal/lock — agent notes

## Before editing

- Wrap every mutating command in `WithLock` (README §Concurrency guard).
- Run the witness before acquiring the flock (README §Concurrency guard).
- Keep read-only operations outside `WithLock` (README §Concurrency guard).
- Keep the deferred release path (README §Public API).

## Navigation

- Entry: `lock.go` (`WithLock`).
- Live-session witness: `internal/tool/claude/witness.go` (`FindActive`).
- Test hook: `lock.go` (`unlockFn`).
- Tests: `lock_test.go` (in-package).
