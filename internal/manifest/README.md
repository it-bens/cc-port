# internal/manifest

## Purpose

Owns the `metadata.xml` wire format and the nine-category enum table. Both
`internal/export` (producer) and `internal/importer` (consumer) depend on
this package; it has no internal project dependencies, so the two sibling
command modules agree on the wire contract through a neutral third party.

## Public API

- **Wire types**
  - `Metadata` — root XML element (`<cc-port>`) wrapping `Info` and placeholders.
  - `Info` — export timestamp plus the per-category include list.
  - `Category` — one `<category name="…" included="…"/>` entry.
  - `Placeholder` — one `<placeholder key="…" original="…" resolvable="…" resolve="…"/>` entry.
- **Category enum table**
  - `CategorySet` — in-memory bool struct (one field per category) used by callers and by `Options.Categories` in `internal/export`.
  - `CategorySpec` — one entry in the enum table: wire name plus `Get`/`Set` accessors onto the matching `CategorySet` field.
  - `AllCategories []CategorySpec` — the ordered source of truth for the nine categories. Slice order is the canonical display and wire order.
  - `BuildCategoryEntries(*CategorySet) []Category` — produces the `<categories>` list in canonical order for `metadata.xml`.
  - `ApplyCategoryEntries([]Category) (CategorySet, error)` — validates a read manifest's category list and returns the matching `CategorySet`; aggregates every missing and every unknown name into one `errors.Join` error.
- **Manifest I/O**
  - `WriteManifest(path string, metadata *Metadata) error`
  - `ReadManifest(path string) (*Metadata, error)`
  - `ReadManifestFromZip(archivePath string) (*Metadata, error)`

## Contracts

### Category manifest

Handled — invariants this package enforces for every write and every read:

- Every export declares all nine `AllCategories` names in `metadata.xml`.
  `BuildCategoryEntries` always emits every entry, so a caller cannot accidentally
  publish a partial list.
- `ApplyCategoryEntries` is the only validator for a parsed manifest. It returns a
  typed `CategorySet` on success and an aggregated error on failure — every missing
  name and every unknown name is surfaced in a single `errors.Join`, so one call
  names every problem.
- `BuildCategoryEntries` and `ApplyCategoryEntries` round-trip stably: for any
  `CategorySet s`, `ApplyCategoryEntries(BuildCategoryEntries(&s))` returns `s`.
- Canonical order is `AllCategories` slice order. Consumers iterate the table in
  that order for display and for deterministic archive layout.

Refused by cc-port — these shapes abort at validation:

- Manifests that declare a subset of the nine category names. All nine must be
  present even when `Included: false`.
- Manifests that declare a name outside `AllCategories`. Unknown names are not
  tolerated with a warning.
- Rewriting the XML wire schema for `Metadata` / `Info` / `Category` /
  `Placeholder`. Field names and XML tags must stay byte-identical on the wire —
  archives already in the wild would otherwise fail to parse.

Not covered — invariants owned elsewhere:

- Session-keyed directory enumeration lives in `claude.SessionKeyedGroups` (see
  [`internal/claude/README.md`](../claude/README.md) §Session-keyed registry).
- Archive zip layout for those groups lives in `transport.SessionKeyedTargets`
  (see [`internal/transport/README.md`](../transport/README.md)).
- File-history snapshot handling is a cross-cutting policy (see
  [`docs/architecture.md`](../../docs/architecture.md) §File-history policy
  (cross-cutting)).

## Tests

Unit tests in `categories_test.go` and `manifest_test.go`. Coverage:
`BuildCategoryEntries`/`ApplyCategoryEntries` round-trip for every category,
aggregated error reporting for missing and unknown names, `WriteManifest` /
`ReadManifest` / `ReadManifestFromZip` round-trip including XML format
stability.
