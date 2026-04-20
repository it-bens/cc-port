# internal/lock — agent notes

## Before editing

- Wrap every mutating command in `WithLock`; keep `findActiveSessions` and `processAlive` unexported (README §Concurrency guard).
- Run the live-session check before acquiring the flock, never after (README §Concurrency guard).
- Do not expose locking for read-only operations (`cc-port move` dry-run, `cc-port export`) (README §Concurrency guard).
- Never replace the `defer` release with an early-return release path (README §Public API).

## Navigation

- Entry: `lock.go:WithLock`.
- Live-session check: `lock.go:findActiveSessions`, `lock.go:processAlive`.
- Test hook: `lock.go:unlockFn` (swap to simulate release failure).
- Tests: `lock_test.go` (in-package).
