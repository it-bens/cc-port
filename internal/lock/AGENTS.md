# internal/lock — agent notes

## Before editing

- Wrap every mutating command in `WithLock` (README §Concurrency guard).
- Run the live-session check before acquiring the flock, never after (README §Concurrency guard).
- Do not expose locking for read-only operations (`cc-port move` dry-run, `cc-port export`). Read-only callers needing the witness list call `FindActive` directly, without taking the flock (README §Public API).
- Keep `processAlive` unexported; it is an implementation detail of `FindActive` (README §Public API).
- Never replace the `defer` release with an early-return release path (README §Public API).

## Navigation

- Entry: `lock.go:WithLock`.
- Live-session witness API: `lock.go:FindActive`, `lock.go:ActiveSession`.
- Liveness probe (internal): `lock.go:processAlive`.
- Test hook: `lock.go:unlockFn` (swap to simulate release failure).
- Tests: `lock_test.go` (in-package).
