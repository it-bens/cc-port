# internal/manifest

## Purpose

Owns the `metadata.xml` wire format and the category enum table.
Both `internal/export` (producer) and `internal/importer` (consumer) depend
on this package. It has no internal project dependencies, so the two sibling
command modules agree on the wire contract through a neutral third party.

## Public API

- **Wire types**
  - `Metadata`: root XML element (`<cc-port>`) wrapping `Info`, placeholders, and the optional `SyncPushedBy` / `SyncPushedAt` sync fields. The two sync fields are written only by `cc-port push` (via `internal/sync`); `cc-port export` archives omit them. `SyncPushedAt` is RFC3339-formatted. Both are strings because `encoding/xml` does not honor `omitempty` for `time.Time`'s zero value.
  - `Info`: export timestamp plus the per-category include list.
  - `Category`: one `<category name="..." included="..."/>` entry.
  - `Placeholder`: one `<placeholder key="..." original="..." [resolvable="..."] [resolve="..."]/>` entry. `resolvable` and `resolve` are omitted from the XML when unset (`*bool` nil / empty string).
- **Category enum table**
  - `CategorySet`: in-memory bool struct (one field per category) used by callers and by `Options.Categories` in `internal/export`.
  - `CategorySpec`: one entry in the enum table: wire name plus `Get`/`Set` accessors onto the matching `CategorySet` field.
  - `AllCategories []CategorySpec`: the ordered source of truth for categories. Slice order is the canonical display and wire order.
  - `BuildCategoryEntries(*CategorySet) []Category`: produces the `<categories>` list in canonical order for `metadata.xml`.
  - `ApplyCategoryEntries([]Category) (CategorySet, error)`: validates a read manifest's category list and returns the matching `CategorySet`. Aggregates every missing and every unknown name into one `errors.Join` error.
- **Manifest I/O**
  - `WriteManifest(path string, metadata *Metadata) error`
  - `ReadManifest(path string) (*Metadata, error)`
  - `ReadManifestFromZip(src io.ReaderAt, size int64) (*Metadata, error)`: parses metadata.xml from a ZIP exposed as `io.ReaderAt + size`. Callers open the source (file, decrypted tempfile, or in-memory bytes) and pass it through; the manifest package is path-agnostic.

## Contracts

### Category manifest

Called by `internal/export` (producer via `BuildCategoryEntries`) and
`internal/importer` (consumer via `ApplyCategoryEntries`).

#### Handled

- Every export declares all `AllCategories` names in `metadata.xml`.
  `BuildCategoryEntries` always emits every entry, so a caller cannot accidentally
  publish a partial list.
- `ApplyCategoryEntries` is the only validator for a parsed manifest.
  It returns a typed `CategorySet` on success and an aggregated error on
  failure. Every missing name and every unknown name surfaces in a single
  `errors.Join` call.
- `BuildCategoryEntries` and `ApplyCategoryEntries` round-trip stably.
  For any `CategorySet s`, `ApplyCategoryEntries(BuildCategoryEntries(&s))`
  returns `s`.
- Canonical order is `AllCategories` slice order. Consumers iterate the
  table in that order for display and deterministic archive layout.

#### Refused

- Manifests that declare a subset of the category names. All must be
  present even when `Included: false`.
- Manifests that declare a name outside `AllCategories`. Unknown names
  hard-fail. No warn-and-continue path.
- Rewriting the XML wire schema for `Metadata` / `Info` / `Category` /
  `Placeholder`. Field names and XML tags must stay byte-identical on the
  wire. Archives in the wild would otherwise fail to parse.

#### Not covered

- Session-keyed directory enumeration lives in `claude.SessionKeyedGroups`
  (see [`internal/claude/README.md`](../claude/README.md)
  §Session-keyed registry).
- Archive zip layout for those groups lives in `transport.SessionKeyedTargets`
  (see [`internal/transport/README.md`](../transport/README.md)).
- File-history snapshot handling is a cross-cutting policy (see
  [`docs/architecture.md`](../../docs/architecture.md)
  §File-history policy (cross-cutting)).

### Manifest read size cap

Both `ReadManifest` and `ReadManifestFromZip` enforce the same 4 MiB cap.

#### Handled

- `ReadManifest` calls `os.Stat` before allocating and rejects files whose
  size exceeds 4 MiB.
- `ReadManifestFromZip` reads at most 4 MiB + 1 byte via `io.LimitReader`
  so it can distinguish an exactly-at-limit file from an over-limit one.
  Both variants return an error naming the source when the cap triggers.

#### Refused

- Manifest documents whose decoded size exceeds `maxManifestBytes` (4 MiB).

#### Not covered

- None at runtime. The cap is fully enforced by this package on every read path.

## Tests

Unit tests in `categories_test.go` and `manifest_test.go`:

- `BuildCategoryEntries`/`ApplyCategoryEntries` round-trip for every category.
- Aggregated error reporting for missing and unknown names.
- `WriteManifest`/`ReadManifest`/`ReadManifestFromZip` round-trip including
  XML format stability.
- Oversize-rejection tests for both `ReadManifest` and `ReadManifestFromZip`
  asserting the 4 MiB cap.

## References

- `encoding/xml`: `go doc encoding/xml` (XXE-safe by design, as the godoc confirms no external entity resolution)
