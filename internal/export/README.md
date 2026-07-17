# internal/export

## Purpose

Generic export orchestration across every selected tool: for each target,
stream its selected categories into one shared archive under a `<tool>/`
prefix and write one `metadata.xml` with one `<tool>` block per target. This
package has no tool-specific knowledge. Every category body, placeholder
discovery rule, and archive-layout decision lives in the owning adapter (for
example `internal/tool/claude`); this package only sequences targets and
assembles the shared manifest.

## Public API

- `Run(ctx context.Context, targets []tool.Target, options *Options) (Result, error)`:
  runs the export. For each target, discovers or reuses its placeholders,
  calls `target.Workspace.Export` against a shared `archive.Sink`, and
  accumulates the result into one manifest. A target whose `Export` reports
  `tool.ErrProjectAbsent` contributes an empty category block (every
  category excluded, no placeholders) rather than failing the whole run.
- `Options`: `ProjectPath`, `Output io.Writer`, `Selected map[string]map[string]bool`,
  `Placeholders map[string][]manifest.Placeholder` (both keyed by tool
  name; a tool absent from `Selected` exports nothing), `SyncPushedBy string`,
  `SyncPushedAt time.Time` (populated only by `cc-port push` via
  `internal/sync`; `cc-port export` leaves both at the zero value and the
  XML elements are omitted), `Reporter progress.Reporter` (nil-handling
  follows `internal/progress/README.md` §Reporter injection).
- `Result`: `Metadata archive.WrittenEntry` (the manifest entry) and
  `ByTool map[string]tool.ExportResult` (each target's own result).

## Contracts

### Category coverage

Called by `cmd/cc-port`. Delegates to `internal/manifest` (see
[`internal/manifest/README.md`](../manifest/README.md) §Category manifest)
for the per-tool enum validation and write helper.

#### Handled

Every archive declares every one of a tool's registered category names in
that tool's `metadata.xml` block, via `manifest.BuildToolCategoryEntries(categoryNames(target.Tool), selected)`.

#### Refused

Hand-rolling a parallel category literal is refused. The only correct path
is `manifest.BuildToolCategoryEntries` fed from the target's own
`Categories()`.

#### Not covered

- Validating that a read archive's category list is correct. That belongs to
  `internal/importer` via `manifest.ApplyToolCategories`.
- What each category actually contains, how bodies are anonymized, and the
  archive's per-tool directory layout. Those are the owning adapter's
  concern; see [`internal/tool/claude/README.md`](../tool/claude/README.md)
  for the Claude instance.

### Per-target sweep semantics

#### Handled

A target reporting `tool.ErrProjectAbsent` from its `Export` call
contributes `tool.ExportResult{Categories: map[string][]tool.ArchiveEntry{}}`
(an explicitly empty, non-nil category map) and an empty tool-manifest
block, rather than aborting the run. Every other target still runs and
still reaches the shared `metadata.xml`.

#### Refused

A target error that is not `tool.ErrProjectAbsent` aborts the whole run; a
partial archive with some targets silently skipped for an unexpected reason
would be worse than a hard failure the caller sees immediately.

#### Not covered

Retrying a failed target. `Run` fails the whole export on the first
unexpected target error; there is no partial-success archive.

## Tests

Unit tests in `export_test.go`, `close_error_test.go`, and
`progress_lifecycle_test.go`. Coverage: multi-target archive assembly, the
`tool.ErrProjectAbsent` empty-block path, the sync-field population and
omission, zip-finalize error propagation via the caller-supplied
`Options.Output`, and the archive/export progress-phase sequencing.

Per-adapter export behavior (category bodies, placeholder discovery,
session-keyed layout, file-history handling) is tested in each adapter
package, not here; see `internal/tool/claude/README.md` §Tests for the
Claude instance.
