# internal/lock — agent notes

Advisory lock + live-session check. See `README.md` for the full contract.

## Before editing

- `Acquire` must run the live-session check after taking the lock — skipping the check to "improve throughput" reintroduces the race the contract guards against (README §Concurrency guard).
- The lock is on `~/.claude/.cc-port.lock` and its release is the kernel's job at process exit; do not add a signal-handler release path that runs before natural cleanup (README §Concurrency guard).
- Do not expose the lock for read-only operations (dry-run `move`, `export`) — those are intentionally unguarded (README §Concurrency guard §Not guarded).

## Navigation

- Entry: `lock.go:Acquire`.
- Handle: `lock.go:Lock`.
- Tests: `lock_test.go`.

Read `README.md` before changing anything under `## Contracts`.
