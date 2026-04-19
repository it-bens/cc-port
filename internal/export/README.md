# internal/export

## Purpose

Produce a cc-port archive from one project: discover path prefixes appearing in bodies, auto-detect placeholder suggestions, write a ZIP containing the chosen categories plus a `metadata.xml` manifest. Also produces a standalone manifest (`export manifest`) that the operator fills in before running `import`.

Not a file-level exporter — this module's unit is one project. Not a path-anonymisation library — the anonymisation heuristics are internal and tied to the manifest schema. The wire format and the category enum table live in [`internal/manifest`](../manifest/README.md); this package is a consumer.

## Public API

- **Entry points**
  - `Run(claudeHome *claude.Home, exportOptions Options) (Result, error)` — full export or manifest-only export, controlled by `Options`.
- **Path discovery**
  - `DiscoverPaths(content []byte) []string` — find path-like tokens inside a body.
  - `GroupPathPrefixes(paths []string) []string` — collapse overlapping prefixes.
  - `AutoDetectPlaceholders(prefixes []string, projectPath, homePath string) []PlaceholderSuggestion` — propose placeholder mappings for all discovered path prefixes: `{{PROJECT_PATH}}` for the project path, `{{HOME}}` for the home path, and `{{UNRESOLVED_N}}` for the rest.
- **Types**
  - `Options`, `Result`, `PlaceholderSuggestion` — export configuration and outputs. `Options.Categories` is a `manifest.CategorySet`.

## Contracts

### Category manifest

Every `cc-port export` archive declares all nine category names in
`metadata.xml`, produced by `manifest.BuildCategoryEntries(&opts.Categories)`.
The importer validates the list with `manifest.ApplyCategoryEntries`, which
hard-fails on any missing or unknown name. The enum table, the write helper,
and the validator all live in [`internal/manifest`](../manifest/README.md)
§Category manifest.

### Anonymisation

Every body written into the archive passes through `applyPlaceholders`, which
substitutes known path prefixes with `{{KEY}}` tokens before the bytes land in
the ZIP. This applies to all 8 non-file-history categories.
The 5 session-keyed zip groups (`todos`, `usage-data/session-meta`,
`usage-data/facets`, `plugins-data`, `tasks`) receive the same
`applyPlaceholders` pass — the privacy guarantee is preserved across all body
types.

The one exception is file-history snapshots: they are archived verbatim with
no anonymisation pass. See §File-history handling (export) for the opt-out
surface.

### Session-keyed zip layout

The five session-keyed categories (`todos`, `usage-data/session-meta`,
`usage-data/facets`, `plugins-data`, `tasks`) are written to the archive by a
single registry-driven loop: `exportSessionKeyed` iterates
`locations.AllFlatFiles()` once, skips groups whose `CategorySet` flag is off,
and resolves each entry's zip prefix and relative-path base from
`transport.SessionKeyedTargets`. There are no per-group helpers — the zip
layout for all five groups is the transport registry, and adding a sixth
group means appending to `claude.SessionKeyedGroups` and
`transport.SessionKeyedTargets` rather than editing this package.

### File-history handling (export)

File-history snapshots are opaque byte streams; see [`docs/architecture.md`](../../docs/architecture.md) §File-history policy (cross-cutting) for the framing that governs every command.

Handled — `cc-port export` (with the `file-history` category enabled) writes each snapshot verbatim into the archive under `file-history/<uuid>/…`. No path anonymisation runs over those bytes. The CLI prints `Warning: N file-history snapshot(s) archived as-is …` to stderr when the count is positive.

Not covered — cases cc-port deliberately does not address:

- **Privacy of exported snapshots.** An archive shared with someone else
  carries the sender's literal project path inside any snapshot that
  quoted it. If a recipient must not see that path, the `file-history`
  category has to be excluded up front — `--file-history=false`, the
  absence of `--file-history` when other `--<category>` flags are set,
  or unchecking the category in the interactive prompt. There is no
  scrub pass between export and archive creation; the category flag is
  the entire opt-out surface.

## Tests

Unit tests in `export_test.go` and `discover_test.go`. Coverage: all-categories export, path anonymisation (including order-independence and boundary collisions), selective category export, history-inclusion rules, path discovery on various body shapes, prefix grouping, auto-placeholder detection. Manifest marshal/unmarshal round-trip and XML format stability live in [`internal/manifest`](../manifest/README.md) §Tests.
