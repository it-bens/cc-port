# internal/move — agent notes

Relocate one project (dry-run + apply). See `README.md` for the full contracts.

## Before editing

- Malformed `history.jsonl` lines are preserved verbatim, not dropped or repaired — do not add a parser-error recovery path (README §Malformed history entries preserved).
- Do not inspect or rewrite file-history snapshot contents; the directory is indexed by UUID and the bytes are opaque (README §File-history handling (move) and root README §File-history policy).
- Every `Apply` path must acquire `~/.claude/.cc-port.lock` before any write and check `~/.claude/sessions/*.json` for live PIDs (see `internal/lock/README.md`).
- Path substring replacement must go through `internal/rewrite.ReplacePathInBytes` — no hand-rolled `strings.ReplaceAll` on user paths (see `internal/rewrite/README.md`).

## Navigation

- Plan: `move.go:DryRun`.
- Apply: `move.go:Apply`.
- Types: `move.go:Options`, `move.go:Plan`.
- Tests: `move_test.go`.

Read `README.md` before changing anything under `## Contracts`.
