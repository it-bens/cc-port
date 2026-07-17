# internal/move — agent notes

## Before editing

- Do not inspect or rewrite file-history snapshot contents; bytes are opaque (README §File-history handling (move)).
- Preserve malformed `history.jsonl` lines verbatim; no parser-error recovery path (README §Malformed history entries preserved).
- This package is generic orchestration; it must never import a tool adapter. Per-tool surfaces (history, sessions, config, etc.) come from each adapter's `MoveSurfaces` (e.g. `internal/tool/claude`). (README §Apply contract)
- Preflight each target's `MoveSurfaces`, then acquire its witness-first flock via `lock.Acquire` in registry order before any target applies; hold all locks through apply and release them in reverse order. (internal/lock/README.md §Concurrency guard)
- Cross-tool rollback does not exist: a target that already completed reflects the true new path even if a later target fails (README §Apply contract).

## Navigation

- Entry: `move.go:DryRun`, `move.go:Apply`.
- Per-target application and rollback: `move.go:applyTarget`.
- Tests: `move_test.go`.
