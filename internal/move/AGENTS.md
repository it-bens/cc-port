# internal/move — agent notes

## Before editing

- Do not inspect or rewrite file-history snapshot contents; bytes are opaque (README §File-history handling (move)).
- Preserve malformed `history.jsonl` lines verbatim; no parser-error recovery path (README §Malformed history entries preserved).
- Wrap every `Apply` path body in `lock.WithLock` before any write (internal/lock/README.md §Concurrency guard).
- Route all path substring replacement through `internal/rewrite.ReplacePathInBytes`; no hand-rolled `strings.ReplaceAll` on user paths (internal/rewrite/README.md §Boundary rules).
- Reuse `rewriteTracked` for every uniform plain-bytes rewrite; do not introduce a separate rollback tracker (README §Apply contract).

## Navigation

- Entry: `move.go:DryRun`, `move.go:Apply`.
- Orchestration and rollback: `execute.go:executeMove`, `execute.go:globalFileTracker`.
- Reference rewrites: `rewrite_global.go`, `rewrite_in_project.go`.
- Dry-run counts: `plan_counts.go`.
- Tests: `move_test.go`, `rewrite_global_test.go`, `restore_internal_test.go`.
