# internal/manifest

## Purpose

Owns the `metadata.xml` wire format and per-tool category validation. Both
`internal/export` (producer, via each tool's `Placeholders`/`Export`) and
`internal/importer` (consumer) depend on this package. It depends on neither
`internal/tool` nor any adapter, so every tool agrees on the wire contract
through a neutral third party.

## Public API

- **Wire types**
  - `Metadata`: root XML element (`<cc-port>`) wrapping `Created` and one
    `Tool` block per tool the export touched, plus the optional
    `SyncPushedBy` / `SyncPushedAt` sync fields. The two sync fields are
    written only by `cc-port push` (via `internal/sync`); `cc-port export`
    archives omit them. `SyncPushedAt` is RFC3339-formatted. Both are
    strings because `encoding/xml` does not honor `omitempty` for
    `time.Time`'s zero value.
  - `(*Metadata).ToolBlock(name string) (Tool, bool)`: looks up one tool's
    block by name.
  - `Tool`: one tool's block inside `metadata.xml` (`<tool name="...">`):
    its `Categories` and `Placeholders`. A tool that did not know the project
    still gets an empty block (every category excluded, no placeholders)
    rather than being omitted.
  - `Category`: one `<category name="..." included="..."/>` entry.
  - `Placeholder`: one `<placeholder key="..." original="..." [resolve="..."]/>`
    entry. `resolve` is omitted from the XML when the value is empty.
    Placeholder keys are scoped to their tool's block; the same key text in
    two different tools' blocks resolves independently.
- **Per-tool category validation**
  - `BuildToolCategoryEntries(declaredNames []string, selected map[string]bool) []Category`:
    produces a `[]Category` in `declaredNames` order for writing into one
    tool's manifest block. `declaredNames` is that tool's `Categories()`
    names in registration order.
  - `ApplyToolCategories(toolName string, declaredNames []string, entries []Category) (map[string]bool, error)`:
    validates one tool's manifest category entries against `declaredNames`
    and returns the selection as `name -> included`. Every declared name
    must appear exactly once.
- **Manifest I/O**
  - `WriteManifest(path string, metadata *Metadata) error`
  - `ReadManifest(path string) (*Metadata, error)`
  - `ReadManifestFromZip(src io.ReaderAt, size int64) (*Metadata, error)`:
    parses `metadata.xml` from a ZIP exposed as `io.ReaderAt` + size. Callers
    open the source (file, materialized tempfile, or in-memory bytes) and
    pass it through; this package never touches paths. Refuses a nil `src`
    with an error naming `MaterializeStage`.

### Errors

- `ErrManifestFileTooLarge`: returned by `ReadManifest` when `metadata.xml`
  on disk exceeds `maxManifestBytes` (4 MiB). Tests assert via `errors.Is`.
- `ErrManifestEntryTooLarge`: returned by `ReadManifestFromZip` when the
  `metadata.xml` zip entry's decoded size exceeds the cap. Tests assert via
  `errors.Is`.
- `ErrNilSource`: returned by `ReadManifestFromZip` when `src` is nil. Tests
  assert via `errors.Is`.
- `UnknownCategoriesError`: returned by `ApplyToolCategories` when a tool's
  manifest block declares category names outside that tool's registered set.
  `Tool` and `Names` carry the offending tool and names; tests assert via
  `errors.As`.
- `MissingCategoriesError`: returned by `ApplyToolCategories` when a tool's
  manifest block omits a category name that tool's registry declares. `Tool`
  and `Names` carry the offending tool and names; tests assert via
  `errors.As`. Joined with `UnknownCategoriesError` via `errors.Join` when
  both classes occur in the same block.
- `DuplicateCategoriesError`: returned by `ApplyToolCategories` when a
  category name occurs more than once in one tool's manifest block.
- `UnregisteredToolError`: returned when a manifest `<tool name="...">` block
  names a tool the caller's registry does not contain at all. `internal/importer`
  wraps this with `%w` and fails the import hard: an unregistered name
  signals an archive built by a newer or foreign cc-port, distinct from a
  registered tool this run simply did not select.

## Contracts

### Category manifest

Called by each tool adapter (producer, via `BuildToolCategoryEntries` inside
its `Export`/`Placeholders` composition in `internal/export`) and
`internal/importer` (consumer, via `ApplyToolCategories`).

#### Handled

- Every export declares every one of a tool's registered category names in
  that tool's `metadata.xml` block. `BuildToolCategoryEntries` always emits
  every name `Categories()` declares, so a caller cannot accidentally publish
  a partial list.
- `ApplyToolCategories` is the only validator for a parsed manifest block. It
  returns a `name -> included` map on success and an aggregated error on
  failure. Every missing name, every unknown name, and every duplicate name
  surfaces in a single `errors.Join` call.
- `BuildToolCategoryEntries` and `ApplyToolCategories` round-trip stably: for
  any selection built and then validated against the same declared names,
  the result matches.
- Canonical order is each tool's `Categories()` registration order. Consumers
  iterate in that order for display and deterministic archive layout.
- A `<tool name="...">` block naming a tool outside the caller's registry is
  a hard failure (`UnregisteredToolError`), never a silent skip; a
  registered tool simply absent from the manifest is a legitimate empty
  result the caller reports and moves past.

#### Refused

- Manifest tool blocks that declare a subset of that tool's category names.
  All must be present even when `Included: false`.
- Manifest tool blocks that declare a name outside that tool's registered
  categories. Unknown names hard-fail; there is no warn-and-continue path.
- Duplicate category names within one tool's block.

#### Not covered

- The registry of tools and their categories. That lives in each `tool.Tool`
  implementation's `Categories()` (see `internal/tool/README.md` §Public API)
  and the process-wide `tool.Set` (see `docs/architecture.md` §The tool
  contract).
- Archive zip layout (the `<tool>/` namespace, entry caps, containment).
  That lives in [`internal/archive/README.md`](../archive/README.md)
  §Contracts.
- File-history snapshot handling is a cross-cutting policy (see
  [`docs/architecture.md`](../../docs/architecture.md)
  §File-history policy (cross-cutting)).

### Manifest read size cap

Both `ReadManifest` and `ReadManifestFromZip` enforce the same 4 MiB cap.

#### Handled

- `ReadManifest` calls `os.Stat` before allocating and rejects files whose
  size exceeds 4 MiB.
- `ReadManifestFromZip` reads at most 4 MiB + 1 byte via `io.LimitReader` so
  it can distinguish an exactly-at-limit file from an over-limit one. Both
  variants return an error naming the source when the cap triggers.

#### Refused

- Manifest documents whose decoded size exceeds `maxManifestBytes` (4 MiB).

#### Not covered

- None at runtime. The cap is fully enforced by this package on every read
  path.

## Tests

Unit tests in `categories_test.go` and `manifest_test.go`:

- `BuildToolCategoryEntries`/`ApplyToolCategories` round-trip, length and
  order preservation, and included-flag fidelity.
- Aggregated error reporting for missing, unknown, and duplicate names within
  one tool's block.
- `UnregisteredToolError`'s message.
- `WriteManifest`/`ReadManifest`/`ReadManifestFromZip` round-trip including
  the `<tool>` block shape, the sync-field round trip and their omission
  when unset, and `(*Metadata).ToolBlock`.
- Oversize-rejection tests for both `ReadManifest` and `ReadManifestFromZip`
  asserting the 4 MiB cap.
- `ReadManifestFromZip`'s nil-source refusal.

## References

- `encoding/xml`: `go doc encoding/xml` (XXE-safe by design, as the godoc confirms no external entity resolution)
