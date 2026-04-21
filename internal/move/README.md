# internal/move

## Purpose

Relocate one project from an old path to a new path. Plans the rewrite (`DryRun`), applies it with copy-verify-delete (`Apply`), and preserves file-history snapshots verbatim.

## Public API

- `DryRun(claudeHome *claude.Home, moveOptions Options) (*Plan, error)`: compute the plan without writing any files.
- `Apply(claudeHome *claude.Home, moveOptions Options) error`: execute the plan with copy-verify-delete, warnings to `Options.WarningWriter`.
- `PlanCategories() []string`: returns a copy of the canonical category ordering so CLI renderers iterate `ReplacementsByCategory` in a stable order.
- `Options`: input parameters for a move operation.
  - `OldPath`, `NewPath`: source and destination project paths.
  - `RewriteTranscripts`: opt-in project-local transcript rewrite.
  - `RefsOnly`: skip the on-disk encoded-dir rename, update references only.
  - `WarningWriter`: warning sink, defaults to `os.Stderr`, unused by `DryRun`.
- `Plan`: dry-run output.
  - `OldProjectDir` / `NewProjectDir`: encoded storage paths.
  - `ReplacementsByCategory map[string]int`: keyed on `planCategories` order, missing keys read as zero.
  - `TranscriptReplacements` (int), `ConfigBlockRekey` and `MoveProjectDir` (booleans).
  - `HistoryMalformedLines []int`: 1-based line numbers that failed to parse.
  - `RulesWarnings []scan.Warning`.

### Internal helpers

`rewriteTracked(path, oldPath, newPath, tracker)` is the shared save -> `rewrite.ReplacePathInBytes` -> `rewrite.SafeWriteFile` sandwich. Every uniform plain-bytes rewrite in `Apply` (settings, the session-keyed groups) routes through it so the rollback tracker sees pre-write bytes consistently.

`snapshotPaths(locations)` enumerates every snapshot path under `locations.FileHistoryDirs`. Contents are never read; only path discovery. The returned length equals the dry-run `plan.ReplacementsByCategory["file-history-snapshots"]`. `DryRun`'s counter and `Apply`'s preservation warning call it so both stay in lock-step off one enumeration. The helper is unexported; the black-box test file reaches it through a `_test.go` binding.

## Contracts

### Malformed history entries preserved

`~/.claude/history.jsonl` is expected to hold one JSON object per line. If a line fails to parse, cc-port cannot reconstruct the intended data from what was written. Repairing broken lines is out of scope.

Callers: `cc-port move` command in `cmd/cc-port`.

#### Handled

- `DryRun` includes a `Warning: history.jsonl has N malformed line(s) at [...]` block in the plan output when any entries fail to parse.
- `Apply` prints `warning: history.jsonl contains N malformed line(s) at [...]` to stderr (or to `Options.WarningWriter`) after `history.jsonl` is rewritten. The rewrite still succeeds. Malformed lines are preserved verbatim and well-formed lines are rewritten normally.

#### Refused

None at runtime. Malformed lines never block the rewrite. They pass through unchanged with well-formed lines rewritten around them.

#### Not covered

- Automatic repair. cc-port does not attempt to reconstruct a broken line, drop it, or quarantine it. The original bytes land back on disk unchanged.
- Detection outside `history.jsonl`. Session transcripts and session subdir files are rewritten as opaque byte streams with path-boundary-aware substitution, not scanned for parse errors.

### Apply contract

`Apply` copies, verifies, and rewrites every file that belongs to the project. Beyond the encoded project directory, history, sessions, and settings, the session-keyed user-wide shapes also receive copy, rewrite, and rollback coverage.

Every session-keyed shape flows through the same `globalFileTracker` rollback as history, sessions, settings, and config. No separate tracker exists for the session-keyed categories.

If rollback cannot restore every saved file, per-file restoration errors are aggregated via `errors.Join` and returned alongside the primary failure.

Callers: `cc-port move --apply` command in `cmd/cc-port`. See `internal/lock/README.md §Concurrency guard` for the live-session check that wraps `Apply`.

#### Handled

- Encoded-dir collision check runs before any write. Moves where old and new paths encode to the same directory, or where the new encoded directory already exists, are refused with a descriptive error.
- `Apply` wraps its body in `lock.WithLock`, which aborts if a Claude Code session is live or another cc-port invocation is running.
- Session-keyed categories: `~/.claude/todos/<sid>-agent-<sid>.json`, `~/.claude/usage-data/session-meta/<sid>.json`, `~/.claude/usage-data/facets/<sid>.json`, `~/.claude/plugins/data/<ns>/<sid>/**`, `~/.claude/tasks/<sid>/**`.

#### Refused

- Moves where old and new paths encode to the same directory are refused before any write.
- Moves where the new encoded directory already exists are refused before any write.
- Moves attempted while a live Claude Code session or concurrent cc-port run holds the advisory lock (see `internal/lock/README.md §Concurrency guard`).

#### Not covered

- `tasks/.lock` and `tasks/.highwatermark` are not copied and not rewritten. They are runtime-only artifacts.

### File-history handling (move)

File-history snapshots under `~/.claude/file-history/<session-uuid>/` are opaque byte streams. cc-port never inspects or rewrites their content. See [`docs/architecture.md`](../../docs/architecture.md) §File-history policy (cross-cutting) for the framing that governs every command. This section covers the move-specific handling.

Callers: `cc-port move` command in `cmd/cc-port`.

#### Handled

- `Apply` leaves every snapshot under the same UUID directory untouched. The old project path may appear inside a snapshot body afterwards. The apply path prints `warning: N file-history snapshot(s) preserved as-is ...` to stderr (or to `Options.WarningWriter`). The dry-run plan reports the preserved count in the same position.

#### Refused

None at runtime. The move never refuses based on snapshot content. Copy is unconditional.

#### Not covered

- Stale path strings inside snapshots after a move. Grepping `~/.claude/file-history/` for the old project path still returns hits after a successful move. This is by design. Rewind continues to work because it resolves by filename, not by content.
- Decoding snapshot UUIDs back to a project. To find the owner of a UUID directory, read the matching `~/.claude/sessions/<uuid>.json` and check its `cwd` field.

## Tests

Unit tests live in `move_test.go` (end-to-end `DryRun`/`Apply` coverage) and `rewrite_global_test.go` (`rewriteTracked` happy path and failure modes). Coverage includes dry-run (with and without transcripts, refs-only, project-not-found), apply (basic, refs-only, with transcripts), encoded-dir collision refusal, live-session refusal, malformed-history reporting, and file-history snapshot preservation.
