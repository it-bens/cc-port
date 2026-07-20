# internal/archive

## Purpose

Implements the ZIP layout every tool shares: a `<tool>/` namespace per
registered tool, decompression caps, `os.Root` containment for staged writes,
and placeholder substitution. Both command packages (`internal/export`,
`internal/importer`) and tool adapters depend on this package; it depends on
neither.

## Public API

- **Reading**
  - `Reader`, `OpenReader(src io.ReaderAt, size int64, caps Caps) (*Reader, error)`:
    opens `src` as a ZIP archive exposed as random-access bytes.
  - `(*Reader).RawEntries() ([]RawEntry, error)`: every entry except
    `metadata.xml`, split into its leading tool-name path segment and the
    tool-relative remainder. Rejects a tool-relative remainder that carries a
    dot segment, an empty segment, or resolves absolute.
  - `RawEntry`: `ToolName`, `Entry`.
  - `Entry`: `Name` (tool-relative), `Modified`;
    `(Entry).ReadAll() ([]byte, error)`, `(Entry).WithAggregateCounter(*AggregateCounter) Entry`.
- **Caps**
  - `Caps`: `MaxEntryBytes`, `MaxAggregateBytes`, `MaxEntries`.
  - `AggregateCounter`, `(*AggregateCounter).Add(n int64) error`,
    `(*AggregateCounter).AddEntry(name string, n int64) error`.
  - `ErrEntryCapExceeded`, `ErrAggregateCapExceeded`, `ErrEntryCountCapExceeded`,
    `EntryCapError`, `AggregateCapError`, `EntryCountCapError`.
- **Classification**
  - `ClassifyPresentKeys(ctx context.Context, entries []RawEntry, candidateKeys []string, maxAggregateBytes int64) (map[string]struct{}, error)`:
    finds which candidate placeholder keys appear as a literal substring in
    at least one entry's body, without retaining any body after inspection.
    Checks `ctx.Err()` before opening each entry, so a canceled context stops
    the walk before decompressing the next body.
- **Placeholder resolution**
  - `ResolvePlaceholdersStream(src io.Reader, dst io.Writer, resolutions map[string]string) error`:
    stream-level token substitution bounded by the longest declared key, not
    by source size.
  - `ResolveEntryBytes(entry Entry, resolutions map[string]string) ([]byte, error)`:
    in-memory substitution, routed through `ResolvePlaceholdersStream` into a
    cap-bounded buffer so expansion is capped incrementally, the same as the
    streaming staging path.
  - `ValidateResolutions(resolutions map[string]string) error`: every
    resolution must be a non-empty absolute path.
- **Writing**
  - `Sink`, `NewSink(writer *zip.Writer, toolName string, placeholders []manifest.Placeholder) *Sink`.
  - `(*Sink).ApplyPlaceholders(data []byte) []byte`: longest-`Original`-first
    substitution.
  - `(*Sink).WriteBytes`, `(*Sink).WriteVerbatim`, `(*Sink).WriteJSONL`: the
    three archive-entry write shapes, each returning a `WrittenEntry`
    (`Name`, `Size`).
  - `Sink` carries no cap field. `WriteJSONL` receives its line cap per call
    from the owning tool.
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
- Placeholder expansion is also capped at `Caps.MaxEntryBytes`, enforced
  incrementally by a shared `countingWriter` as substituted bytes are
  written, not after the whole resolved body is allocated. `ResolveEntryBytes`
  (in-memory) and `StageSibling` (streaming) both route through this writer,
  so a small archive whose resolution value is repeated many times cannot
  pre-allocate an oversized buffer before the cap fires. The aggregate never
  includes expanded output bytes.
- The capped reader feeds `AggregateCounter` incrementally with decompressed
  input at the decompression point. `ReadAll`, streaming staging, and
  classification use that same reader, so the cap can reject an entry before
  its whole body decodes. The counter includes classification reads and refuses
  once its total passes `Caps.MaxAggregateBytes` (4 GiB by default).
- `OpenReader` checks the central directory's entry count against
  `Caps.MaxEntries` (200,000 by default) before any staging begins, closing
  an axis neither byte cap reaches: an archive of hundreds of thousands of
  zero-byte entries, each still allocating a `RawEntry`, a `Staged` record,
  and a temp inode, can stay far under both byte caps. `MaxEntries` set to
  zero disables the check.

**Refused.**

- Entries whose declared or observed size exceeds the active per-entry cap:
  `EntryCapError` names the entry, its size, and the limit.
- An archive whose running aggregate exceeds the active cap: `AggregateCapError`
  names the entry that tipped the total over.
- An archive whose central directory carries more entries than the active
  `MaxEntries` cap: `EntryCountCapError` names the observed count and the
  limit.

**Not covered.**

- Compression-ratio bombs below both caps. A body that decompresses to just
  under the per-entry limit, repeated just under the aggregate limit's entry
  count, is accepted; the caps bound absolute bytes, not compression ratio.

### os.Root containment

**Handled.**

- `RawEntries` rejects any tool-relative remainder that fails
  `validArchiveEntryName` (a dot segment, an empty segment, or an absolute
  path), checked on the raw, uncleaned path before any consumer routes on
  the entry name. This closes a gap where `filepath.Clean` would collapse a
  traversal like `memory/../secret.jsonl` down to `secret.jsonl` before the
  old check ever saw the `..` segment, so the tool-prefix routing decision
  (made on the raw path) and the staged location (computed after cleaning)
  could disagree.
- `StageSibling` runs the same entry-name guard again immediately before any
  staged write, as defense in depth.
- `assertWithinRoot` validates the complete cleaned final relative path, not
  only its parent. It rejects `.`, `..`, leading `../`, and absolute paths
  before writing, then creates non-root parents through an `os.Root` handle.
- The sibling temp path is computed from the symlink-resolved parent of the
  final destination (`StagingTempPath`), so temp and final always share a
  filesystem and `os.Rename` stays intra-filesystem.

**Refused.**

- Any archive entry whose tool-relative name would escape the staging base:
  `ErrZipSlip`, raised at `RawEntries` before any routing runs, and again at
  `StageSibling` as defense in depth. Named with the offending path (and,
  at `StageSibling`, the base).
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
- The reader peeks up to the longest declared key inside a fixed 64 KiB
  buffered window, and only ever attempts a match after reading a leading
  `{` byte. `internal/manifest` bounds every declared key to 4 KiB and to
  the `"{{" + non-empty inner segment + "}}"` grammar at manifest
  validation, before a manifest-declared key ever reaches this package
  (see [`internal/manifest/README.md`](../manifest/README.md) §Placeholder
  key validation), so a declared key can never exceed the peek window or
  land outside the shape this package's matching is anchored on.
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

- Whether a key is one the caller's manifest actually declared for this
  tool; provenance is the caller's responsibility.
- Rejecting a malformed key. Matching is anchored on a leading `{` byte
  and compares by exact byte prefix (see the peek-window bullet above). A
  key that does not begin with `{` is never attempted. A key that does
  begin with `{` but is not `{{NAME}}`-shaped can still match if its
  exact bytes appear in the body. Neither function raises an error for a
  non-matching or malformed key. A key declared in a manifest is always
  `{{NAME}}`-shaped: `internal/manifest` already refused any other shape
  at read time (see [`internal/manifest/README.md`](../manifest/README.md)
  §Placeholder key validation). A caller that hands either function a
  hand-built resolutions map directly bypasses that gate.

## Tests

Unit tests in `archive_test.go`, `resolve_test.go`, `mtime_test.go`, and the
internal `applymtime_internal_test.go` and `validarchiveentryname_internal_test.go`.
Coverage: tool-prefix split and the malformed-prefix refusal, per-entry and
entry-count cap rejection, `ClassifyPresentKeys` finding only referenced
keys, the placeholder stream's start/middle/end and read-boundary-straddling
cases, `ResolveEntryBytes` bounding expansion incrementally before the
resolved body is fully allocated, zip-slip rejection at both `RawEntries`
and `StageSibling` (including the absolute-path and empty-segment
branches), `validArchiveEntryName`'s raw-segment table, and `Sink`'s three
write shapes each preserving (or, for a zero timestamp, omitting) the
source `Modified` time.
