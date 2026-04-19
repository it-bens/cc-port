# internal/move

## Purpose

Relocate one project from an old path to a new path. Plans the rewrite (`DryRun`), applies it with copy-verify-delete (`Apply`), emits warnings for rules files and malformed history entries, and preserves file-history snapshots verbatim.

Not a cross-project operation — one invocation targets exactly one `(oldPath → newPath)` pair. Batch moves are expressed as repeated `move --apply` calls, each wrapped in `lock.WithLock`.

## Public API

- **Entry points**
  - `DryRun(claudeHome *claude.Home, moveOptions Options) (*Plan, error)` — compute the plan without writing.
  - `Apply(claudeHome *claude.Home, moveOptions Options) error` — execute the plan; copy-verify-delete, warnings to `Options.WarningWriter`.
- **Types**
  - `Options` — input: `OldPath`, `NewPath`, `RewriteTranscripts` (opt-in project-local transcript rewrite), `RefsOnly` (skip the on-disk encoded-dir rename, update references only), `WarningWriter` (warning sink; defaults to `os.Stderr`, unused by `DryRun` which surfaces warnings through `Plan`).
  - `Plan` — dry-run output: `OldProjectDir` / `NewProjectDir` (encoded storage paths), `ReplacementsByCategory map[string]int` (keyed on the canonical `planCategories` ordering — `history`, `sessions`, `settings`, the five `claude.SessionKeyedGroups` names in order, then `file-history-snapshots`; missing keys read as zero), `TranscriptReplacements` count, `ConfigBlockRekey` and `MoveProjectDir` booleans, `HistoryMalformedLines []int` (1-based line numbers that failed to parse), `RulesWarnings []scan.Warning`.
  - `PlanCategories() []string` — returns a copy of the canonical category ordering so CLI renderers iterate `ReplacementsByCategory` in a stable order without reaching into the package's private `planCategories` slice.
- **Internal helpers worth naming**
  - `rewriteTracked(path, oldPath, newPath, tracker)` — the shared save → `rewrite.ReplacePathInBytes` → `rewrite.SafeWriteFile` sandwich. Every uniform plain-bytes rewrite in Apply (settings, the five session-keyed groups) routes through this helper so the rollback tracker sees the pre-write bytes consistently.

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

### Apply contract

`cc-port move --apply` copies, verifies, and rewrites every file that belongs
to the project. Beyond the encoded project directory, history, sessions, and
settings, the following session-keyed user-wide shapes receive copy + rewrite
+ rollback:

- `~/.claude/todos/<sid>-agent-<sid>.json`
- `~/.claude/usage-data/session-meta/<sid>.json`
- `~/.claude/usage-data/facets/<sid>.json`
- `~/.claude/plugins/data/<ns>/<sid>/**`
- `~/.claude/tasks/<sid>/**`

`tasks/.lock` and `tasks/.highwatermark` are runtime-only and skipped during
move — they are not copied and not rewritten.

All five shapes flow through the same `globalFileTracker` rollback as the
existing history/sessions/settings/config files. No separate tracker is
introduced for the session-keyed categories.

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
