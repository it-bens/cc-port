# internal/export

## Purpose

Produce a cc-port archive from one project: discover path prefixes appearing in bodies, auto-detect placeholder suggestions, write a ZIP containing the chosen categories plus a `metadata.xml` manifest. Also produces a standalone manifest (`export manifest`) that the operator fills in before running `import`.

Not a file-level exporter — this module's unit is one project. Not a path-anonymisation library — the anonymisation heuristics are internal and tied to the manifest schema.

## Public API

- **Entry points**
  - `Run(claudeHome *claude.Home, exportOptions Options) (Result, error)` — full export or manifest-only export, controlled by `Options`.
- **Path discovery**
  - `DiscoverPaths(content []byte) []string` — find path-like tokens inside a body.
  - `GroupPathPrefixes(paths []string) []string` — collapse overlapping prefixes.
  - `AutoDetectPlaceholders(prefixes []string, projectPath, homePath string) []PlaceholderSuggestion` — propose placeholder mappings for prefixes that are not the project path itself.
- **Manifest I/O**
  - `WriteManifest(path string, metadata *Metadata) error`
  - `ReadManifest(path string) (*Metadata, error)`
  - `ReadManifestFromZip(archivePath string) (*Metadata, error)`
- **Types**
  - `Options`, `Result`, `CategorySet`, `PlaceholderSuggestion` — export configuration and outputs.
  - `Metadata`, `Info`, `Category`, `Placeholder` — manifest XML types.

## Contracts

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

Unit tests in `export_test.go`, `discover_test.go`, `manifest_test.go`. Coverage: all-categories export, path anonymisation (including order-independence and boundary collisions), selective category export, history-inclusion rules, path discovery on various body shapes, prefix grouping, auto-placeholder detection, manifest marshal/unmarshal round-trip, manifest XML format stability.
