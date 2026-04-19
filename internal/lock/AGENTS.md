# internal/lock — agent notes

Advisory lock + live-session check. See `README.md` for the full contract.

## Before editing

- `WithLock` is the sole public entry point; `acquire`/`release`/`lock` are unexported on purpose so a new mutating command cannot skip the release path (README §Public API).
- `WithLock` must run the live-session check via `acquire` before calling `fn` — skipping the check to "improve throughput" reintroduces the race the contract guards against (README §Concurrency guard).
- The lock is on `~/.claude/.cc-port.lock` and its release is the kernel's job at process exit; do not add a signal-handler release path that runs before natural cleanup (README §Concurrency guard).
- Do not expose the lock for read-only operations (dry-run `move`, `export`) — those are intentionally unguarded (README §Concurrency guard §Not guarded).

## Navigation

- Entry: `lock.go:WithLock`.
- Internals: `lock.go:acquire`, `lock.go:release`, `lock.go:lock` (unexported).
- Tests: `lock_test.go` (in-package).

Read `README.md` before changing anything under `## Contracts`.
