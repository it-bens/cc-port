# internal/lock — agent notes

Advisory lock + live-session check. See `README.md` for the full contract.

## Before editing

- `WithLock` is the sole public entry point; `findActiveSessions` and `processAlive` are unexported on purpose so a new mutating command cannot bypass the live-session check (README §Public API).
- `WithLock` must run the live-session check before calling `fn` — skipping the check to "improve throughput" reintroduces the race the contract guards against (README §Concurrency guard).
- The lock is on `~/.claude/.cc-port.lock` and its release is the kernel's job at process exit; do not add a signal-handler release path that runs before natural cleanup (README §Concurrency guard).
- Do not expose the lock for read-only operations (dry-run `move`, `export`) — those are intentionally unguarded (README §Concurrency guard).
- `WithLock` releases the flock via `defer`; never replace the deferred release with an imperative `Unlock` call before `fn` returns. The `defer` is what closes the panic-recovery gap (README §Concurrency guard).

## Navigation

- Entry: `lock.go:WithLock`.
- Live-session check: `lock.go:findActiveSessions`, `lock.go:processAlive`.
- Test hook: `lock.go:unlockFn` (swap to simulate release failure).
- Tests: `lock_test.go` (in-package).

Read `README.md` before changing anything under `## Contracts`.
