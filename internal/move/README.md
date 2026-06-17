# internal/move

## Purpose

Relocate one project from an old path to a new path. Plans the rewrite (`DryRun`), applies it with copy-verify-delete (`Apply`), and preserves file-history snapshots verbatim.

## Public API

- `DryRun(ctx context.Context, claudeHome *claude.Home, moveOptions Options) (*Plan, error)`: compute the plan without writing any files.
- `Apply(ctx context.Context, claudeHome *claude.Home, moveOptions Options) error`: execute the plan with copy-verify-delete, emitting progress and warnings through `Options.Reporter`.
- `PlanCategories() []string`: returns a copy of the canonical category ordering so CLI renderers iterate `ReplacementsByCategory` in a stable order.
- `Options`: input parameters for a move operation.
  - `OldPath`, `NewPath`: source and destination project paths.
  - `RewriteTranscripts`: opt-in project-local transcript rewrite.
  - `RefsOnly`: skip the on-disk encoded-dir rename, update references only.
  - `Reporter`: progress and warning sink, unused by `DryRun`; nil-handling follows `internal/progress/README.md` §Reporter injection.
- `Plan`: dry-run output.
  - `OldProjectDir` / `NewProjectDir`: encoded storage paths.
  - `ReplacementsByCategory map[string]int`: keyed on `planCategories` order, missing keys read as zero.
  - `TranscriptReplacements` and `MemoryReplacements` (ints), `ConfigBlockRekey` and `MoveProjectDir` (booleans).
  - `HistoryMalformedLines []int`: 1-based line numbers that failed to parse.
  - `RulesReport scan.Report`.

### Internal helpers

`rewriteTracked(path, oldPath, newPath, tracker)` is the shared save -> `rewrite.ReplacePathInBytes` -> `rewrite.SafeWriteFile` sandwich. Every uniform plain-bytes rewrite in `Apply` routes through it so the rollback tracker sees pre-write bytes consistently: `rewriteUserWideFiles` iterates `claude.UserWideRewriteTargets` (settings.json, plugins/installed_plugins.json, plugins/known_marketplaces.json). The session-keyed-groups loop iterates `locations.AllFlatFiles()` through `rewriteTrackedPreservingMtime`, which wraps `rewriteTracked` and restores each file's pre-rewrite modification time. See §Source mtime preservation (move).

`snapshotPaths(ctx, locations)` enumerates every snapshot path under `locations.FileHistoryDirs`. Contents are never read; only path discovery. The returned length equals the dry-run `plan.ReplacementsByCategory["file-history-snapshots"]`. `DryRun`'s counter and `Apply`'s preservation warning call it so both stay in lock-step off one enumeration. `ctx` is checked at the top of the outer loop and inside the `listFilesRecursive` walk so a long enumeration aborts within one iteration. The helper is unexported; the black-box test file reaches it through a `_test.go` binding.

`rewriteHistoryFile` picks the rollback snapshot route by `history.jsonl` size. Files under `siblingBackupThreshold` (1 MiB) take the in-memory route via `tracker.save`; larger files take the on-disk `saveToSibling` route so the original never lands whole in RAM. Both routes share `rewrite.StreamHistoryJSONL` for the actual rewrite, so behavior parity between the two sizes is guaranteed.

## Contracts

### Malformed history entries preserved

`~/.claude/history.jsonl` is expected to hold one JSON object per line. If a line fails to parse, cc-port cannot reconstruct the intended data from what was written. Repairing broken lines is out of scope.

Callers: `cc-port move` command in `cmd/cc-port`.

#### Handled

- `DryRun` includes a `Warning: history.jsonl has N malformed line(s) at [...]` block in the plan output when any entries fail to parse.
- `Apply` emits a `history.jsonl contains N malformed line(s) at [...]` warning through `Options.Reporter` after `history.jsonl` is rewritten. The rewrite still succeeds. Malformed lines are preserved verbatim and well-formed lines are rewritten normally.

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
- User-wide categories iterated via `claude.UserWideRewriteTargets`: `~/.claude/settings.json`, `~/.claude/plugins/installed_plugins.json`, `~/.claude/plugins/known_marketplaces.json`. Each is stat-gated; missing files skip without error.
- Copied project-dir bodies have the old encoded storage dir swapped for the new one via a second `rewrite.ReplacePathInBytes` pass in `rewritePathsPreservingMtime`, alongside the real-path rewrite. The pass covers the transcript / session-subdir tree under `--rewrite-transcripts` and the memory files unconditionally.

#### Refused

- Moves where old and new paths encode to the same directory are refused before any write.
- Moves where the new encoded directory already exists are refused before any write.
- Moves attempted while a live Claude Code session or concurrent cc-port run holds the advisory lock (see `internal/lock/README.md §Concurrency guard`).

#### Not covered

- `tasks/.lock` and `tasks/.highwatermark` are not copied and not rewritten. They are runtime-only artifacts.

### Source mtime preservation (move)

A move rewrites the embedded project path in every file, and additionally swaps the encoded storage dir in the copied project-dir tree (transcripts and memory), leaving the session data otherwise unchanged. Transcripts, memory files, and session-keyed flat files keep their source modification times so the move does not reorder Claude Code's mtime-sorted `/resume` picker.

Callers: `cc-port move --apply` command in `cmd/cc-port`.

#### Handled

- Transcripts and memory files: copied by `internal/fsutil.CopyDir`, which restores mtime per [`internal/fsutil/README.md`](../fsutil/README.md) §Symlink replication for CopyDir, then rewritten by `rewritePathsPreservingMtime`, which restores the mtime the copy carried over.
- Session-keyed flat files (`todos`, `usage-data`, `plugins-data`, `tasks`): rewritten in place through `rewriteTrackedPreservingMtime`, matching how `cc-port import` preserves the same categories (see [`internal/importer/README.md`](../importer/README.md) §Source mtime preservation).

#### Refused

None at runtime. An `os.Stat` or `os.Chtimes` failure aborts the rewrite and the rollback tracker unwinds partial work.

#### Not covered

- `history.jsonl`, `~/.claude.json`, `settings.json`, and `~/.claude/sessions/*.json`: rewritten with a fresh modification time. These are merged or genuinely edited globals, not verbatim session files, so they inherit the write-time mtime the way an import treats merge results.
- File-history snapshots: never rewritten, so their mtime survives untouched. See §File-history handling (move).

### File-history handling (move)

File-history snapshots under `~/.claude/file-history/<session-uuid>/` are opaque byte streams. cc-port never inspects or rewrites their content. See [`docs/architecture.md`](../../docs/architecture.md) §File-history policy (cross-cutting) for the framing that governs every command. This section covers the move-specific handling.

Callers: `cc-port move` command in `cmd/cc-port`.

#### Handled

- `Apply` leaves every snapshot under the same UUID directory untouched. The old project path may appear inside a snapshot body afterwards. The apply path emits a `note: N file-history snapshot(s) preserved verbatim ...` warning through `Options.Reporter`. The dry-run plan reports the preserved count in the same position.

#### Refused

None at runtime. The move never refuses based on snapshot content. Copy is unconditional.

#### Not covered

- Stale path strings inside snapshots after a move. Grepping `~/.claude/file-history/` for the old project path still returns hits after a successful move. This is by design. Rewind continues to work because it resolves by filename, not by content.
- Decoding snapshot UUIDs back to a project. To find the owner of a UUID directory, read the matching `~/.claude/sessions/<uuid>.json` and check its `cwd` field.

## Tests

Unit tests live in `move_test.go` (end-to-end `DryRun`/`Apply` coverage) and `rewrite_global_test.go` (`rewriteTracked` happy path and failure modes). Coverage includes dry-run (with and without transcripts, refs-only, project-not-found), apply (basic, refs-only, with transcripts), encoded-dir collision refusal, live-session refusal, malformed-history reporting, file-history snapshot preservation, source mtime preservation on session files, and fresh mtime on merged globals.
