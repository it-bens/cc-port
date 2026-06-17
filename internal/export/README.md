# internal/export

## Purpose

Produces a cc-port archive from one project: discover path prefixes, propose placeholder mappings, write a ZIP with the chosen categories plus a `metadata.xml` manifest.

This module's unit is one project, not the file system at large. The wire format and the category enum table live in [`internal/manifest`](../manifest/README.md). This package is a consumer.

## Public API

- `Run(ctx context.Context, claudeHome *claude.Home, exportOptions *Options) (Result, error)`: full export, controlled by `Options`. Writes the archive bytes into `Options.Output`. The caller owns the writer's lifecycle; `Run` does not open or close any file.
- `DiscoverPaths(content []byte) []string`: find path-like tokens inside a body.
- `AutoDetectPlaceholders(prefixes []string, projectPath, homePath string) []PlaceholderSuggestion`: assign `{{PROJECT_PATH}}` and `{{HOME}}` to matching prefixes; unknown prefixes are dropped.
- `DiscoverPlaceholders(content []byte, projectPath, homePath string) []PlaceholderSuggestion`: canonical composition for `cc-port export`. Runs `DiscoverPaths`, filters candidates against the project and home anchors, and emits at most two suggestions.
- `Options`, `Result`, `PlaceholderSuggestion`: export configuration and outputs. `Options.Output` is the `io.Writer` archive bytes are written into; `Options.Categories` is a `manifest.CategorySet`. `Options.Reporter progress.Reporter` is the export progress sink; nil-handling follows `internal/progress/README.md` §Reporter injection.
- `Options.SyncPushedBy` (`string`) and `Options.SyncPushedAt` (`time.Time`) are optional fields populated only by `cc-port push` (via `internal/sync`). When non-empty / non-zero, `buildMetadata` writes them to `metadata.xml` as `<sync-pushed-by>` and `<sync-pushed-at>` (RFC3339, UTC). `cc-port export` callers leave them at the zero value and the elements are omitted.

## Contracts

### Category coverage

Called by `cmd/cc-port`. Delegates to `internal/manifest` (see [`internal/manifest/README.md`](../manifest/README.md) §Category manifest) for the enum table, write helper, and validator.

#### Handled

Every archive declares all category names in `metadata.xml` via `manifest.BuildCategoryEntries(&opts.Categories)`.

#### Refused

Hand-rolling a parallel category literal is refused. The only correct path is `manifest.BuildCategoryEntries`.

#### Not covered

Validation that a read archive's category list is correct. That belongs to `internal/importer` via `manifest.ApplyCategoryEntries`.

### Anonymisation

Called by `cmd/cc-port` for every full export. `applyPlaceholders` is also the `internal/export`-private anonymisation path for all non-file-history categories.

#### Handled

Every body written to the archive passes through `applyPlaceholders` before hitting the ZIP. This applies to sessions, memory, history, config, and every session-keyed group (`todos`, `usage-data/session-meta`, `usage-data/facets`, `plugins-data`, `tasks`).

`{{PROJECT_DIR}}` is declared unconditionally by the cmd layer from `claude.Home.ProjectDir(projectPath)`, not discovered, because the encoded storage reference lives in session-subdir bodies that placeholder discovery does not scan. `applyPlaceholders` substitutes it before `{{HOME}}` via longest-first ordering. When the reference is absent the declared key is unused.

Placeholder replacement is order-independent across runs: a re-export of the same project produces the same placeholder set. Covered by `export_test.go:TestExport_PathAnonymization_OrderIndependent`.

File-history snapshots are the one exception. See §File-history handling (export).

#### Refused

A partial-scrub pass on file-history bytes is refused. The category flag is the only opt-out surface.

#### Not covered

Privacy of snapshot content inside an exported archive. If the archive is shared, the recipient sees literal project paths embedded in any snapshot that quoted them. Excluding the `file-history` category is the only mitigation.

### Session-keyed zip layout

Used internally by `exportSessionKeyed`.

#### Handled

The session-keyed groups (`todos`, `usage-data/session-meta`, `usage-data/facets`, `plugins-data`, `tasks`) are written by two cooperating functions. `collectSessionKeyedPairs` iterates `locations.AllFlatFiles()` once, buckets files by category, and derives the distinct category order from `claude.SessionKeyedGroups` in registry order. `exportSessionKeyed` loops over those categories, skips any whose `CategorySet` flag is off, and opens one sub-phase per selected category, rolling the two usage-data groups into a single `usage-data` sub-phase. Each entry's zip prefix and relative-path base come from `transport.SessionKeyedTargets`. There are no per-group helpers.

#### Refused

Hard-coding a zip prefix or home base directory in this package is refused. All layout comes from `transport.SessionKeyedTargets`.

#### Not covered

Adding a new session-keyed group. That requires appending to `claude.SessionKeyedGroups` and `transport.SessionKeyedTargets`, not editing this package.

### File-history handling (export)

Governed by the cross-cutting policy in [`docs/architecture.md`](../../docs/architecture.md) §File-history policy (cross-cutting).

#### Handled

When `file-history` is enabled, each snapshot is written verbatim under `file-history/<uuid>/...`. No path anonymisation runs. `Run` returns a `Result` whose `FileHistory` slice carries one `ArchiveEntry` per snapshot. The CLI prints a warning when the slice is non-empty.

#### Refused

Inspecting or rewriting snapshot bytes is refused. Snapshots are opaque user-file bytes.

#### Not covered

Privacy of exported snapshots. An archive shared with someone else carries literal project paths inside any snapshot that quoted them. Excluding the category up front (`--file-history=false`, or omitting it when other categories are explicitly selected) is the entire opt-out surface.

### Source mtime preservation

Used by `cc-port import` and `cc-port pull` to restore the chronological ordering of imported files (Claude Code's `/resume` picker orders sessions by mtime).

#### Handled

Every verbatim archive entry carries the source file's mtime in `FileHeader.Modified`. `streamJSONLEntry` and `streamVerbatimEntry` stat their open source and pass `ModTime()` through to the zip-write helpers. Five categories are covered: session JSONLs, memory files, sub-agent files, every session-keyed group (`todos`, `usage-data/session-meta`, `usage-data/facets`, `plugins-data`, `tasks`), and file-history snapshots.

The encoding is Go's `archive/zip` Info-ZIP `extTimeExtraID` extra field at whole-second precision. Sub-second source mtimes are truncated. The import side at [`internal/importer/README.md`](../importer/README.md) §Source mtime preservation reads `Modified` back and applies it to the staged file.

#### Refused

None at runtime. A `Stat` failure on the open source aborts the entry write.

#### Not covered

`metadata.xml`, `history.jsonl`, and `config.json` carry no per-file timestamp. Each is synthesized or merged at export time, so the archive entry's `Modified` is left zero. The import side reads zero as "no mtime carried" and the staged file inherits its natural import-time mtime.

## Tests

Unit tests in `export_test.go`, `discover_test.go`, `close_error_test.go`, and `file_history_errors_test.go`. Coverage: all-categories export, path anonymisation (order-independence, boundary collisions), selective category export, history-inclusion rules, file-history pipeline, zip-finalize and per-entry write fault injection via the caller-supplied `Options.Output`, path discovery, anchored placeholder discovery, auto-placeholder detection.

`history_line_cap_test.go` drives the `MaxHistoryLine` cap through `Run`. The invariant is owned by [`internal/claude/README.md`](../claude/README.md) §History line cap.

Manifest marshal/unmarshal round-trip and XML format stability live in [`internal/manifest`](../manifest/README.md) §Tests.
