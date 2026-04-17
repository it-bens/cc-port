# internal/move

## Purpose

Relocate one project from an old path to a new path. Plans the rewrite (`DryRun`), applies it with copy-verify-delete (`Apply`), emits warnings for rules files and malformed history entries, and preserves file-history snapshots verbatim.

Not a cross-project operation — one invocation targets exactly one `(oldPath → newPath)` pair. Batch moves are expressed as repeated `move --apply` calls with a lock acquired per call.

## Public API

- **Entry points**
  - `DryRun(claudeHome *claude.Home, moveOptions Options) (*Plan, error)` — compute the plan without writing.
  - `Apply(claudeHome *claude.Home, moveOptions Options) error` — execute the plan; copy-verify-delete, warnings to `Options.WarningWriter`.
- **Types**
  - `Options` — input: `OldPath`, `NewPath`, `RewriteTranscripts` (opt-in project-local transcript rewrite), `RefsOnly` (skip the on-disk encoded-dir rename, update references only), `WarningWriter` (warning sink; defaults to `os.Stderr`, unused by `DryRun` which surfaces warnings through `Plan`).
  - `Plan` — dry-run output: `OldProjectDir` / `NewProjectDir` (encoded storage paths), replacement counts (`HistoryReplacements`, `SessionFileReplacements`, `SettingsReplacements`, `TranscriptReplacements`), `ConfigBlockRekey` and `MoveProjectDir` booleans, `FileHistorySnapshotsPreserved` count, `HistoryMalformedLines []int` (1-based line numbers that failed to parse), `RulesWarnings []scan.Warning`.

## Contracts

### Malformed history entries preserved

`~/.claude/history.jsonl` is expected to be one JSON object per line. If
Claude Code wrote a partial line (crash, disk full, concurrent-write
race) or another tool corrupted the file, some lines will fail to parse.
These entries predate any cc-port invocation; the move did not create
them and cannot reconstruct the intended data from what was written.
Repairing them is out of scope — cc-port is a relocator, not a history
repair tool.

Surfaced by cc-port — both paths report malformed lines with their
1-based line numbers so the user can inspect or delete them manually:

- `cc-port move` (dry-run) includes a `Warning: history.jsonl has N
  malformed line(s) at […]` block in the plan output when any entries
  fail to parse.
- `cc-port move --apply` prints the same warning to stderr (or to the
  `move.Options.WarningWriter` supplied by callers) after the rewrite
  completes. The rewrite still succeeds — malformed lines are preserved
  verbatim, well-formed lines are rewritten normally.

Not covered — cases cc-port deliberately does not address:

- **Automatic repair.** cc-port does not attempt to reconstruct a broken
  line, drop it, quarantine it, or re-parse fragments. The original
  bytes land back on disk unchanged.
- **Detection outside `history.jsonl`.** The same class of corruption
  can in principle affect session transcripts (`*.jsonl` under the
  project directory) or session subdir files, but cc-port does not scan
  those for parse errors — they are rewritten as opaque byte streams
  with path-boundary-aware substitution.

### File-history handling (move)

File-history snapshots under `~/.claude/file-history/<session-uuid>/` are opaque byte streams; cc-port never inspects or rewrites their content. See [`docs/architecture.md`](../../docs/architecture.md) §File-history policy (cross-cutting) for the framing that governs every command. This section covers the move-specific handling.

Handled — these calls copy verbatim and warn:

- `cc-port move` (apply or dry-run) leaves every snapshot under the same
  UUID directory untouched. The old project path may still appear inside
  a snapshot body afterwards; the apply path prints
  `warning: N file-history snapshot(s) preserved as-is …` to stderr (or
  to `move.Options.WarningWriter`) and the dry-run plan reports the
  preserved count in the same position.

Not covered — cases this approach does not address:

- **Stale path strings inside snapshots after a move.** Grepping
  `~/.claude/file-history/` for the old project path still returns hits
  after a successful move. This is by design: editing snapshot bytes
  means substring-rewriting arbitrary user-file content, which is the
  class of risk the previous binary-detection heuristic tried (and
  sometimes failed) to guard against. Rewind continues to work because
  it resolves by filename, not by content.
- **Decoding snapshot UUIDs back to a project.** Snapshot directories
  are named by session UUID. To find the owner of a UUID directory,
  read the matching `~/.claude/sessions/<uuid>.json` and look at its
  `cwd` field — cc-port does not index file-history in the reverse
  direction.

## Tests

Unit tests in `move_test.go`. Coverage: dry-run (with and without transcripts, refs-only, project-not-found), apply (basic, refs-only, with transcripts), encoded-dir collision refusal, live-session refusal (see `internal/lock/README.md` §Concurrency guard), malformed-history reporting and warning emission, file-history snapshot preservation.
