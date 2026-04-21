# internal/export

## Purpose

Produces a cc-port archive from one project: discover path prefixes, propose placeholder mappings, write a ZIP with the chosen categories plus a `metadata.xml` manifest.

This module's unit is one project, not the file system at large. The wire format and the category enum table live in [`internal/manifest`](../manifest/README.md). This package is a consumer.

## Public API

- `Run(claudeHome *claude.Home, exportOptions Options) (Result, error)`: full export, controlled by `Options`.
- `DiscoverPaths(content []byte) []string`: find path-like tokens inside a body.
- `GroupPathPrefixes(paths []string) []string`: collapse overlapping prefixes.
- `AutoDetectPlaceholders(prefixes []string, projectPath, homePath string) []PlaceholderSuggestion`: propose `{{PROJECT_PATH}}`, `{{HOME}}`, and `{{UNRESOLVED_N}}` mappings for all discovered prefixes.
- `Options`, `Result`, `PlaceholderSuggestion`: export configuration and outputs. `Options.Categories` is a `manifest.CategorySet`.

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

Placeholder replacement is order-independent across runs: a re-export of the same project produces the same placeholder set. Covered by `export_test.go:TestExport_PathAnonymization_OrderIndependent`.

File-history snapshots are the one exception. See §File-history handling (export).

#### Refused

A partial-scrub pass on file-history bytes is refused. The category flag is the only opt-out surface.

#### Not covered

Privacy of snapshot content inside an exported archive. If the archive is shared, the recipient sees literal project paths embedded in any snapshot that quoted them. Excluding the `file-history` category is the only mitigation.

### Session-keyed zip layout

Used internally by `exportSessionKeyed`.

#### Handled

The session-keyed groups (`todos`, `usage-data/session-meta`, `usage-data/facets`, `plugins-data`, `tasks`) are written by one registry-driven loop. `exportSessionKeyed` iterates `locations.AllFlatFiles()` once and skips groups whose `CategorySet` flag is off. Each entry's zip prefix and relative-path base come from `transport.SessionKeyedTargets`. There are no per-group helpers.

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

## Tests

Unit tests in `export_test.go` and `discover_test.go`. Coverage: all-categories export, path anonymisation (order-independence, boundary collisions), selective category export, history-inclusion rules, path discovery, prefix grouping, auto-placeholder detection.

`history_line_cap_test.go` drives the `MaxHistoryLine` cap through `Run`. The invariant is owned by [`internal/claude/README.md`](../claude/README.md) §History line cap.

Manifest marshal/unmarshal round-trip and XML format stability live in [`internal/manifest`](../manifest/README.md) §Tests.
