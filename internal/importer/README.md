# internal/importer

## Purpose

Apply a cc-port export archive to a target path. Validates every placeholder, pre-resolves the staging filesystem topology, and writes each destination through a sibling `*.cc-port-import.tmp`. Promotes all staged temps atomically via `SafeRenamePromoter`.

The reverse direction lives in `internal/export`. This module assumes the cc-port manifest and placeholder contract. It rejects any ZIP that does not satisfy them.

## Public API

- `Run(ctx context.Context, claudeHome *claude.Home, importOptions Options) (*Result, error)`: import an archive end-to-end. Reads the archive via `zip.NewReader` on `Options.Source`. Wraps in `lock.WithLock`, validates resolutions, checks for conflicts, pre-resolves staging parents, reads the archive, classifies placeholders, stages, promotes, and rolls back on failure. Returns `Result.RulesReport` carrying the post-promote rules-file scan against `TargetPath`. Refuses a nil `Options.Source` with an error naming `MaterializeStage`; cmd-layer chains compose `pipeline.MaterializeStage` to satisfy the contract.
- `Result`: success-path output of `Run`. `RulesReport scan.Report` carries the rules-file scan; cmd renders it via `cmd/cc-port`'s `renderRulesReport`.
- `ResolvePlaceholders(unresolved []string, fromManifest *manifest.Metadata) (map[string]string, error)`: compose a resolution map from the plan's unresolved keys and optional `--from-manifest` metadata. Filters implicit keys and merges manifest-known non-empty values. Any non-implicit key that remains unresolved after the merge surfaces as `MissingResolutionsError`. The byte-level token substitution helper used inside `Run` is unexported.
- `IsImplicitKey(key string) bool`: predicate exposing the implicit-key set without leaking the constants. Covers `{{PROJECT_PATH}}` (supplied from the import target argument) and `{{HOME}}` (supplied via `resolveHomeAnchor` from `os.UserHomeDir()` on the import machine). Callers refuse user-supplied resolutions for these keys and treat them as already-resolved when computing unresolved sets.
- `ClassifyPlaceholders(bodies [][]byte, declared []manifest.Placeholder, resolutions map[string]string) (missing, undeclared []string)`: diff the archive's declared placeholders against the caller's resolutions and the bodies' embedded tokens. Returns alphabetically sorted slices of missing declared keys and undeclared upper-snake tokens.
- `ResolvePlaceholdersStream(src io.Reader, dst io.Writer, resolutions map[string]string) error`: reader-to-writer placeholder substitution. Peak memory is bounded by the longest placeholder key, not by the body size. Used by the staging paths that write directly to a sibling temp (sessions, memory, session-keyed categories).
- `ValidateResolutions(resolutions map[string]string) error`: syntactic validation of caller-supplied resolutions (non-empty, absolute paths only).
- `CheckConflict(encodedProjectDir string) error`: refuse the import if the encoded target directory already exists. Also refuse when existence cannot be determined (e.g. a permission error on an intermediate component). Only a clean "does not exist" returns `nil`.
- `BuildHistoryBytes(existing []byte, appends [][]byte) []byte`: pure byte concatenation used by staging to compute the merged history bytes before atomic promote. No I/O, no lock.
- `MergeProjectConfigBytes(existingData []byte, configPath, targetPath string, blockData []byte) ([]byte, error)`: splice a project block into an existing `.claude.json` body. Preserves key order, indent, and trailing newlines via `sjson`. `configPath` is used only in error messages.
- `Options`: import configuration. `Source io.ReaderAt` and `Size int64` describe the archive bytes, and the importer constructs `*zip.Reader` directly and never opens files. `TargetPath`, `Resolutions`, and the unexported `renameHook` test seam stay as before. `HomePath string` is the recipient's home directory, supplied by the cmd layer via `resolveHomeAnchor`, and injected into the working resolution map by `withImplicitAnchors`. `Source` must be non-nil, and cmd-layer chains compose `pipeline.MaterializeStage` to populate it.

### Errors

- `ErrEncodedDirCollision`: returned by `CheckConflict` (and surfaced via `Run`'s "conflict check" wrap) when the encoded project directory already exists. Tests assert via `errors.Is`.
- `ErrStatProjectDirectory`: returned by `CheckConflict` when the encoded project directory's existence cannot be determined (permission error on an intermediate path component). Wraps the underlying os error via `%w` chain.
- `ErrEntryCapExceeded`: returned by `Run` when an archive entry's uncompressed size exceeds the per-entry cap (`maxZipEntryBytes`, 512 MiB). Fires from both the declared-size check and the post-decode counter.
- `ErrAggregateCapExceeded`: returned by `Run` when the sum of decompressed bytes across all archive entries exceeds `maxArchiveUncompressedBytes` (4 GiB). Fires from both classify (pass one) and stage (pass two).
- `UnknownArchiveEntryError`: typed error returned by `Run` when an archive entry's name does not match any known prefix. `Name` carries the offending entry; tests assert via `errors.As`.
- `ErrZipSlip`: returned by `Run` when an archive entry's resolved relative path would land outside its staging base. Fires from the os.Root containment guard inside `assertWithinRoot` and `streamResolveIntoRoot`.
- `ErrStagingFailed`: returned by `Run` when the staging jail cannot be established (staging-base mkdir or os.OpenRoot failure). Distinct from `ErrZipSlip`: signals destination-side I/O failure, not a containment violation.
- `ErrSourceNil`: returned by `Run` when `Options.Source` is nil. The wrapping message hints that the caller's pipeline likely missed `MaterializeStage`.
- `MissingResolutionsError`: typed error reporting declared placeholder keys present in the archive that the caller did not resolve. `Keys` carries the offending key list, alphabetically sorted; tests assert via `errors.As`.
- `UndeclaredTokensError`: typed error reporting `{{UPPER_SNAKE}}` tokens that appear in the archive but are not listed in the manifest's placeholders. `Tokens` carries the offending token list, alphabetically sorted.
- `InvalidConfigJSONError`: typed error returned by `MergeProjectConfigBytes` when `existingData` is not valid JSON. `Path` carries the config file path the caller passed.

## Contracts

### Import contract

Caller: `cmd/cc-port`.

`cc-port import` treats every archive as a closed contract. Every placeholder token a body contains must be accounted for before any destination is written.

The pre-flight gate in `importer.go:Run` scans every ZIP entry and diffs against the manifest's declared placeholders and the caller's resolutions. Any mismatch aborts the import before any write. A refused import leaves the destination untouched: no partial writes, no dangling staging temps.

Every destination is staged at a sibling `*.cc-port-import.tmp` path and promoted via `os.Rename`:

- `<encoded-project-dir>.cc-port-import.tmp` to `<encoded-project-dir>`
- `~/.claude/history.jsonl.cc-port-import.tmp` to `~/.claude/history.jsonl`
- `~/.claude.json.cc-port-import.tmp` to `~/.claude.json`
- per-entry file-history temps to their final `~/.claude/file-history/...` destinations

`internal/rewrite/rewrite.go:SafeRenamePromoter` drives the promote step and owns the rollback. If any rename fails, every earlier rename is reversed from the saved pre-promote bytes of each replaced destination.

#### Handled

- Refused import: no write has occurred and destination is untouched.
- Promote failure after partial rename: `SafeRenamePromoter` reverses each already-promoted entry to its pre-import state.
- Implicit anchors: `{{PROJECT_PATH}}` is supplied by `importer.Run` from the import target argument, and `{{HOME}}` is supplied by the cmd layer from the recipient machine's `os.UserHomeDir()` via `resolveHomeAnchor`. `IsImplicitKey` reports both. Both are treated as resolved even when absent from the caller's resolution map, and user-supplied resolutions for either key are refused at the cmd layer.

#### Refused

These paths abort before any write:

- Archive embeds a declared placeholder in at least one body whose key has no matching resolution. The key is absent from `Options.Resolutions` and is not implicit. `MissingResolutionsError` lists every missing key in alphabetical order.
- Archive body contains a `{{KEY}}` that the manifest does not declare. `UndeclaredTokensError` lists every undeclared key in alphabetical order.
- Archive entries whose names escape the staging base (containing `..` components or an absolute-path prefix). The `os.Root` handle rejects any path that would land outside the base. No temp file is created.
- Archive entries whose decompressed size exceeds `maxZipEntryBytes` (512 MiB). Both passes check the declared `UncompressedSize64` up front and the actual post-decode byte count after streaming. A misdeclared size does not slip through.
- Archives whose aggregate decompressed payload exceeds `maxArchiveUncompressedBytes` (4 GiB). Enforced in both passes of `loadArchive` so an archive that misdeclares per-entry sizes between passes cannot slip through the cap.

#### Not covered

- **Pre-fix archives with declared but unresolved keys.** Archives produced before the resolver simplification are refused, regardless of any `resolvable` attribute the legacy manifest carried. Every declared key embedded in a body must resolve unless it is implicit. Migration: re-export with the fixed pipeline.
- **Undeclared exotic token shapes in bodies.** The tamper-defense scan is grammar-bounded. It does not catch lowercase, punctuated, or whitespace-bearing tokens in bodies that the manifest fails to declare. See §Placeholder handling below.

### Placeholder handling

`resolve.go:ClassifyPlaceholders` decides which keys the archive embeds, which still need a resolution, and which tokens the archive embeds without declaring them.

The manifest is authoritative. Every key cc-port's export path embeds is also written into `metadata.xml` as a `<placeholder>` entry. The importer iterates the declared set and tests presence with a literal `bytes.Contains` per key.

No body grammar is parsed on the resolution path. The exporter's key shape is correctly classified by construction.

`internal/rewrite/rewrite.go:FindPlaceholderTokens` is retained only as a tamper-defense scan. Upper-snake `{{KEY}}` tokens in bodies that are absent from the manifest are reported as undeclared, and such an archive is refused before any write.

A scanner that parsed placeholder tokens directly out of body bytes would have to commit to a grammar. Any grammar narrow enough to avoid false positives on JSON or Markdown `{{...}}` would miss exotic keys a future exporter might emit.

The manifest-is-authoritative design avoids that tradeoff entirely on the resolution path. The tamper-defense scan accepts the grammar bound as a deliberate residual risk.

#### Handled

- Any declared key embedded in at least one body, with a matching resolution: substituted at resolve time.
- Any declared key embedded in at least one body, with no resolution, that is not implicit: flagged missing, archive refused.
- Any declared key that does not appear in any body: ignored. The archive may legitimately publish metadata about keys it considered but did not embed.
- An upper-snake `{{UPPER_SNAKE}}` token embedded in a body that the manifest does not declare: reported as undeclared, archive refused.

#### Refused

- Missing resolutions for declared keys (see §Import contract §Refused).
- Archive body contains an undeclared upper-snake token (tamper-defense scan trigger).

#### Not covered

- **Undeclared exotic-shape tokens in bodies.** A body token with a lowercase, punctuated, or whitespace-bearing key (e.g. `{{my-weird.key}}`) that is not declared in `metadata.xml` is invisible to the tamper-defense scan. It survives verbatim on disk: neither flagged nor substituted. Widening the scanner's grammar to catch these would produce false positives on `{{...}}` content in transcripts (Handlebars, Mustache, Jinja). Tool-produced archives are not affected since cc-port's export path publishes every key it embeds. Hand-crafted archives that want the full contract must declare every embedded key in the manifest.

### Atomic staging

`cc-port import` makes every destination visible all-or-nothing by staging each write at a sibling `*.cc-port-import.tmp` path and promoting it with `os.Rename`. `os.Rename` is atomic only within a single filesystem.

A bare-sibling temp path sits on the wrong side of the boundary when a destination's parent is a symlink to another volume (e.g. `~/.claude/file-history` on an external disk). That would fail mid-import with `EXDEV`.

Project, memory, file-history, and session-keyed writes route through an `os.Root` handle opened on the staging base. A path-escaping entry is rejected before any write. `streamResolveIntoRoot` opens the root and writes through it. `assertWithinRoot` is the containment gate for the sibling-temp writers (`stageFileHistoryFromZip`, `stageSessionKeyedFileFromZip`) that must keep the layout `SafeRenamePromoter` requires.

`importer.go:stagingTempPath` resolves the parent directory of each final destination through any symlinks before forming the temp path. Temp and final are then siblings of the resolved parent and always share a filesystem.

The walk uses `fsutil.ResolveExistingAncestor` (see [`internal/fsutil/README.md`](../fsutil/README.md) §Absolute-path contract for `ResolveExistingAncestor`). The longest existing prefix is symlink-resolved. Any missing tail is re-attached unchanged so `MkdirAll` creates it on the resolved filesystem.

`importer.go:checkStagingFilesystems` runs this resolution once up front. It covers the encoded project directory, `history.jsonl`, `.claude.json`, the file-history base, and the session-keyed bases (`todos/`, `usage-data/session-meta/`, `usage-data/facets/`, `plugins/data/`, `tasks/`). Any failures are aggregated into a single error before the archive is read or any temp is written.

#### Handled

- All destinations on the same filesystem (the common macOS and Linux layout with everything under the home directory).
- Any subset of destinations whose parent directory is a symlink crossing a filesystem boundary (e.g. `~/.claude/file-history` pointed at an external volume). The temp is staged on the external volume alongside its final, and `os.Rename` remains intra-filesystem.
- Destinations whose parent directory does not exist yet. The ancestor walk finds the closest existing prefix, resolves it, and `MkdirAll` creates the missing components on that filesystem.

#### Refused

These paths abort at preflight with a single aggregated error:

- A destination's symlinked parent is broken or otherwise unresolvable (`EvalSymlinks` returns a non-`ENOENT` error).
- A destination's parent ancestor walk fails with a non-`ENOENT` stat error (permission denied on an intermediate component, etc.).

#### Not covered

- **Final destination is itself a cross-filesystem symlink.** If a final destination is a cross-filesystem symlink, `CheckConflict`/merge decides by existing-file rules, not symlink topology. The affected destinations are `~/.claude/projects/<encoded>`, `~/.claude/history.jsonl`, and `~/.claude.json`. For the project directory, `CheckConflict` refuses when the encoded directory already exists, so a pre-existing symlinked leaf never reaches the rename. A symlinked `history.jsonl` or `.claude.json` leaf routes through `os.Rename` on the symlink's parent filesystem. If the symlink straddles a boundary, the promote fails and `SafeRenamePromoter` rolls back.
- **Filesystem topology changes mid-import.** The preflight resolves parents once. A concurrent operation that replaces a resolved parent with a cross-filesystem symlink between preflight and promote can still produce `EXDEV` at rename time. The promoter rolls back and the import aborts, but the friendly preflight error does not fire.

The rollback surface is driven by `SafeRenamePromoter`. See `internal/rewrite/README.md` §Boundary rules for the promoter's public API. The import flow itself owns the staging temp-path resolution in `importer.go:stagingTempPath`.

#### Session-keyed prefix arms

The session-keyed prefixes are staged alongside the existing ones:

- `todos/` staged to `~/.claude/todos/`
- `usage-data/session-meta/` staged to `~/.claude/usage-data/session-meta/`
- `usage-data/facets/` staged to `~/.claude/usage-data/facets/`
- `plugins-data/` staged to `~/.claude/plugins/data/`
- `tasks/` staged to `~/.claude/tasks/`

The prefix-to-destination mapping is owned by `transport.SessionKeyedTargets` (see [`internal/transport/README.md`](../transport/README.md)). This package does not hard-code any of the prefixes. Dispatch inside `stageArchiveEntries` runs one loop (`dispatchSessionKeyed`) that walks the transport registry and routes an entry to `stageSessionKeyedFileFromZip` on the first `ZipPrefix` match.

There are no per-group staging helpers. The unified `importPlan.sessionKeyedStagedFiles` slice accumulates every session-keyed entry regardless of group, and the same slice drives promotion and cleanup.

Promotion order after the encoded project directory, history, config, and file-history entries follows `transport.SessionKeyedTargets` order: todos, usage-data/session-meta, usage-data/facets, plugins-data, tasks.

`importPlan.cleanupTemps()` returns `error`. It aggregates `os.Remove` and `os.RemoveAll` failures via `errors.Join` so the caller logs a single diagnostic on an already-failed import path.

### Strict archive contract

`cc-port import` validates the manifest's category list before reading any ZIP entry. The validator is `manifest.ApplyCategoryEntries` (see [`internal/manifest/README.md`](../manifest/README.md) §Category manifest). The importer only drives it and surfaces its aggregated error.

#### Handled

- Valid manifests with all known category names: categories applied, archive read proceeds.
- Archives with some categories marked `included="false"`: entries for those categories are not present and the importer does not attempt to stage them.

#### Refused

- Unknown category name in the manifest: reported in a single `errors.Join` error by `manifest.ApplyCategoryEntries` before any ZIP entry is read.
- Missing category name in the manifest: same error path.
- Any ZIP entry whose path does not match a known prefix (`sessions/`, `memory/`, `history/history.jsonl`, `file-history/`, `config.json`, `todos/`, `usage-data/session-meta/`, `usage-data/facets/`, `plugins-data/`, `tasks/`): rejected before any write.

#### Not covered

- **Archives from older or modified cc-port versions with unrecognised entries.** There is no tolerant fallback. `stageUnknownEntry` was removed. Such archives are refused in full and partial staging does not occur.

### File-history handling (import)

File-history snapshots are opaque byte streams. See [`docs/architecture.md`](../../docs/architecture.md) §File-history policy (cross-cutting) for the framing that governs every command.

#### Handled

- `cc-port import` streams snapshots back to disk byte-for-byte. No placeholder resolution runs over their bodies: the opacity contract says snapshot bytes are not inspected or rewritten, and that applies on the import side the same as it does on the export and move sides.

#### Refused

- None at runtime. File-history entries reach `stageFileHistoryFromZip` only after the closed-contract pre-flight in §Import contract has passed.

#### Not covered

- None at runtime. The opaque-bytes policy means content interpretation is out of scope.

## Tests

Unit tests in `importer_test.go` and `resolve_test.go`. Coverage:

- basic round-trip.
- no staging temps left behind.
- refusal on unresolved and undeclared keys.
- atomic rollback on failure.
- conflict refusal on pre-existing encoded directories.
- zip-slip rejection (`..`-escaping entry).
- absolute-entry rejection.
- oversized-entry rejection (512 MiB per-entry cap). The default run uses a 1 MiB test override so CI can exercise the guard on a few-megabyte archive. A 600 MiB sibling in `importer_large_test.go` exercises the real cap and runs only under `go test -tags large`.
- aggregate-size rejection (4 GiB aggregate cap) with the same small-override plus `-tags large` sibling split.
- streaming placeholder replacer: passthrough when resolutions are empty, single and boundary positions, longest-match across prefix keys, and token straddling a buffered-reader fill boundary (chunk-reader wrapper).

Fuzz target in `resolve_fuzz_test.go`. `FuzzApplyResolutions` asserts no-panic, empty-map identity, absent-key identity, and the length-accounting invariant `len(out) == len(in) + count*(len(value)-len(key))`.

The stronger "key bytes never survive" claim is not asserted. Under adversarial inputs `bytes.ReplaceAll` can reconstruct a key at a substitution boundary. That cannot happen under the production `{{UPPER_SNAKE}}` grammar where values are absolute filesystem paths.

Seed inputs run as deterministic subtests under `go test ./...`. The unbounded mutation loop is local-only.

## References

- `os.Root`: local authoritative: `go doc os.Root`, online supplement: https://pkg.go.dev/os#Root
- `io.LimitReader`: local authoritative: `go doc io.LimitReader`, online supplement: https://pkg.go.dev/io#LimitReader
