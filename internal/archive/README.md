# internal/archive

## Purpose

Implements the ZIP layout every tool shares: a `<tool>/` namespace per
registered tool, decompression caps, `os.Root` containment for staged writes,
and placeholder substitution. Both command packages (`internal/export`,
`internal/importer`) and tool adapters depend on this package; it depends on
neither.

## Public API

- **Reading**
  - `Reader`, `OpenReader(src io.ReaderAt, size int64) (*Reader, error)`:
    opens `src` as a ZIP archive exposed as random-access bytes.
  - `(*Reader).RawEntries() ([]RawEntry, error)`: every entry except
    `metadata.xml`, split into its leading tool-name path segment and the
    tool-relative remainder.
  - `RawEntry`: `ToolName`, `Entry`.
  - `Entry`: `Name` (tool-relative), `Modified`;
    `(Entry).ReadAll() ([]byte, error)`, `(Entry).WithAggregateCounter(*AggregateCounter) Entry`.
- **Caps**
  - `Caps`: `MaxEntryBytes`, `MaxAggregateBytes`.
  - `SetCaps(Caps) (restore func())`: overrides the active cap set; test-only,
    production code never calls it.
  - `AggregateCounter`, `(*AggregateCounter).Add(n int64) error`,
    `(*AggregateCounter).AddEntry(name string, n int64) error`.
  - `ErrEntryCapExceeded`, `ErrAggregateCapExceeded`, `EntryCapError`,
    `AggregateCapError`.
- **Classification**
  - `ClassifyPresentKeys(entries []RawEntry, candidateKeys []string) (map[string]struct{}, error)`:
    finds which candidate placeholder keys appear as a literal substring in
    at least one entry's body, without retaining any body after inspection.
- **Placeholder resolution**
  - `ResolvePlaceholdersStream(src io.Reader, dst io.Writer, resolutions map[string]string) error`:
    stream-level token substitution bounded by the longest declared key, not
    by source size.
  - `ApplyResolutions(content []byte, resolutions map[string]string) []byte`:
    in-memory substitution via `bytes.ReplaceAll`.
  - `ValidateResolutions(resolutions map[string]string) error`: every
    resolution must be a non-empty absolute path.
- **Writing**
  - `Sink`, `NewSink(writer *zip.Writer, toolName string, placeholders []manifest.Placeholder) *Sink`.
  - `(*Sink).ApplyPlaceholders(data []byte) []byte`: longest-`Original`-first
    substitution.
  - `(*Sink).WriteBytes`, `(*Sink).WriteVerbatim`, `(*Sink).WriteJSONL`: the
    three archive-entry write shapes, each returning a `WrittenEntry`
    (`Name`, `Size`).
  - `WriteMetadata(writer *zip.Writer, metadata *manifest.Metadata) (WrittenEntry, error)`:
    writes `metadata.xml` at the archive root, outside any tool's namespace.
- **Staging (import side)**
  - `Staged`: `Temp`, `Final`. `StagedSet`, `(*StagedSet).Add`, `(*StagedSet).All`.
  - `StagingTempPath(finalPath string) (string, error)`: the symlink-resolved
    sibling temp path for `finalPath`.
  - `StageSibling(baseDir, relativePath string, entry Entry, resolutions map[string]string, perm os.FileMode, mtime time.Time) (Staged, int64, error)`:
    streams `entry`'s capped body into a sibling temp beside `relativePath`
    resolved under `baseDir`, gated by an `os.Root` containment check.
- **Errors**: `ErrZipSlip`, `ErrStagingFailed`, `ErrMalformedEntryName`,
  `ErrNilSource`.

## Contracts

### Entry decompression caps

**Handled.**

- Every entry's declared `UncompressedSize64` is checked before opening a
  reader; a body already over `Caps.MaxEntryBytes` (512 MiB by default)
  never streams at all.
- The post-decode byte count is checked again after streaming, so an entry
  that misdeclares its size in the central directory cannot slip through the
  declared-size check.
- `AggregateCounter` tallies every entry an archive read observes, including
  bytes read for a placeholder classification pass, and refuses once the
  running total passes `Caps.MaxAggregateBytes` (4 GiB by default). Per-entry
  caps alone do not stop a crafted archive with many entries at
  just-under-the-limit size from exhausting memory and disk in aggregate.

**Refused.**

- Entries whose declared or observed size exceeds the active per-entry cap:
  `EntryCapError` names the entry, its size, and the limit.
- An archive whose running aggregate exceeds the active cap: `AggregateCapError`
  names the entry that tipped the total over.

**Not covered.**

- Compression-ratio bombs below both caps. A body that decompresses to just
  under the per-entry limit, repeated just under the aggregate limit's entry
  count, is accepted; the caps bound absolute bytes, not compression ratio.

### os.Root containment

**Handled.**

- `StageSibling` and the internal `assertWithinRoot` open an `os.Root`
  handle on the staging base directory before any write and resolve
  `relativePath` through it, so a path containing `..` components or an
  absolute-path prefix that would land outside the base is rejected before a
  temp file is created.
- The sibling temp path is computed from the symlink-resolved parent of the
  final destination (`StagingTempPath`), so temp and final always share a
  filesystem and `os.Rename` stays intra-filesystem.

**Refused.**

- Any archive entry whose resolved relative path would escape the staging
  base: `ErrZipSlip`, named with the offending path and base.
- A staging base that cannot be created or opened as an `os.Root`:
  `ErrStagingFailed`, distinct from `ErrZipSlip` because it signals
  destination-side I/O failure, not a containment violation.

**Not covered.**

- Symlink races between the containment check and the write. `os.Root`
  resolves once per call; a filesystem mutated concurrently by another
  process between check and write is out of scope, matching every other
  atomic-rename primitive in cc-port.

### Per-tool prefixes

**Handled.**

- `RawEntries` splits every non-`metadata.xml` entry on its first `/` into a
  tool name and a tool-relative remainder, so every downstream consumer
  (manifest validation, `Stage` routing) works with tool-relative names and
  never re-derives the prefix.
- `Sink` adds the `<toolName>/` prefix automatically on every write; a tool's
  `Export` implementation never constructs the prefix itself.

**Refused.**

- An entry name with no `/` separator, an empty leading segment, or nothing
  after the separator: `ErrMalformedEntryName`, naming the raw archive path.

**Not covered.**

- Validating that the tool name in an entry's prefix is a registered tool.
  That check belongs to the caller (`internal/importer`), which has the
  registry this package does not depend on.

### Placeholder machinery

**Handled.**

- `ResolvePlaceholdersStream` is boundary-free by design: the cc-port
  `{{UPPER_SNAKE}}` token grammar is self-delimiting, so no path-component
  check is needed the way `internal/rewrite`'s raw substring rewriter needs
  one. Peak memory is bounded by the longest declared key, not by body size,
  and a token is never split across a read boundary.
- Match order is deterministic: resolutions are visited longest-key-first so
  a key that is a textual prefix of another still resolves to the longest
  match.
- `ClassifyPresentKeys` inspects one entry's body at a time behind the shared
  caps and discards it immediately after, so peak memory during
  classification is bounded by one entry, not by the archive's total size.

**Refused.**

- `ValidateResolutions` rejects any resolution that is empty or not an
  absolute path, before any substitution runs.

**Not covered.**

- Parsing or validating the `{{KEY}}` grammar itself against a schema.
  `ApplyResolutions` and `ResolvePlaceholdersStream` substitute exactly the
  keys they are given; whether a key is one the caller's manifest actually
  declared is the caller's responsibility.

## Tests

Unit tests in `archive_test.go`, `resolve_test.go`, `mtime_test.go`, and the
internal `applymtime_internal_test.go`. Coverage: tool-prefix split and the
malformed-prefix refusal, per-entry cap rejection, `ClassifyPresentKeys`
finding only referenced keys, the placeholder stream's start/middle/end and
read-boundary-straddling cases, zip-slip rejection at `StageSibling`, and
`Sink`'s three write shapes each preserving (or, for a zero timestamp,
omitting) the source `Modified` time.
