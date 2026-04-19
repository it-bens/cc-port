# internal/move — agent notes

Relocate one project (dry-run + apply). See `README.md` for the full contracts.

## Before editing

- Malformed `history.jsonl` lines are preserved verbatim, not dropped or repaired — do not add a parser-error recovery path (README §Malformed history entries preserved).
- Do not inspect or rewrite file-history snapshot contents; the directory is indexed by UUID and the bytes are opaque (README §File-history handling (move) and docs/architecture.md §File-history policy (cross-cutting)).
- Every `Apply` path wraps its body in `lock.WithLock` before any write (see `internal/lock/README.md` §Concurrency guard).
- Path substring replacement must go through `internal/rewrite.ReplacePathInBytes` — no hand-rolled `strings.ReplaceAll` on user paths (see `internal/rewrite/README.md`).
- Session-keyed user-wide files (todos, usage-data, plugins-data, tasks) flow
  through the same `globalFileTracker` rollback as history/sessions/settings/
  config — do not introduce a separate tracker (README §Apply contract). The
  shared `rewriteTracked` helper performs the save → replace → write
  sandwich; reuse it rather than duplicating the sequence per group.

## Navigation

- Plan: `move.go:DryRun`.
- Apply: `move.go:Apply`.
- Types: `move.go:Options`, `move.go:Plan`.
- Tests: `move_test.go`.

Read `README.md` before changing anything under `## Contracts`.
