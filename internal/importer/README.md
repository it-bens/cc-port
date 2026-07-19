# internal/importer

## Purpose

Generic import orchestration across every selected tool: validate the
archive's manifest, merge placeholder resolutions, stage every tool's
entries, promote every staged file across every tool as one all-or-nothing
batch, then finalize. This package has no tool-specific knowledge; per-tool
staging and merge logic lives in each adapter's `Stage`/`Finalize` (for
example `internal/tool/claude`).

The reverse direction lives in `internal/export`. This package assumes the
cc-port manifest and placeholder contract and rejects any archive that does
not satisfy it.

## Public API

- `Run(ctx context.Context, allTools *tool.Set, targets []tool.Target, options *Options) (*Result, error)`:
  imports an archive end-to-end. `allTools` is the full registry, used to
  distinguish an unregistered archive-entry tool prefix (hard failure) from a
  registered tool this run did not select (silently skipped as absent from
  the manifest). `targets` is the narrowed, already-opened set this run
  imports into, in registration order: every target's advisory lock is
  acquired (nested, registry order) before any archive byte is read, and
  every target's `Finalize` runs, still under lock, after every tool's
  staged files have promoted as one batch.
- `Options`: `Source io.ReaderAt`, `Size int64` (the archive bytes; `Run`
  constructs the `*zip.Reader` directly and never opens files itself),
  `TargetPath string`, `FromManifest *manifest.Metadata` (optional per-tool
  placeholder `Resolve` overrides read from a `--from-manifest` file; nil
  means no override), `Reporter progress.Reporter` (nil-handling follows
  `internal/progress/README.md` §Reporter injection).
- `Result`: `SkippedTools []string` (tools selected for this run whose
  manifest carried no `<tool>` block: the archive simply has no data for
  them) and `Warnings map[string][]string` (non-fatal `Finalize` notices,
  keyed by tool wire name).

### Errors

- `ErrSourceNil`: returned by `Run` when `Options.Source` is nil. The
  wrapping message hints that the caller's pipeline likely missed
  `MaterializeStage`.
- `ErrNoTargets`: returned by `Run` when no tool was selected to import into.
- `UnknownEntryToolError`: returned when an archive entry's leading path
  segment names a tool this binary does not register at all. Distinct from a
  registered tool simply not being selected this run (silently skipped): an
  unrecognized name signals an archive built by a newer or foreign cc-port.
- `MissingResolutionsError`: reports, within one tool's namespace, declared
  placeholder keys that are present in the archive but have no resolution.
  `Tool` and `Keys` carry the offending tool and key list, alphabetically
  sorted; tests assert via `errors.As`.
- `UndeclaredResolutionKeysError`: reports `--from-manifest` resolutions for
  keys a tool's archive block does not declare. `Tool`, `Keys`, and
  `Surface` carry the context; tests assert via `errors.As`.
- `ImplicitKeyOverrideError`: reports `--from-manifest` resolutions for keys
  the target derives as implicit anchors. `Tool`, `Keys`, and `Surface` carry
  the context; tests assert via `errors.As`.

`manifest.UnknownCategoriesError` / `manifest.MissingCategoriesError` /
`manifest.DuplicateCategoriesError` (per-tool category validation failures)
are returned by `manifest.ApplyToolCategories` and reached here via `%w`
wrapping. `manifest.UnregisteredToolError` (a manifest `<tool>` block naming
an unregistered tool entirely) is a type `internal/manifest` only defines.
`ApplyToolCategories` never returns it, and this package's own registry
validation (`verifyManifestTools`) constructs and wraps it directly. See
[`internal/manifest/README.md`](../manifest/README.md) §Errors for all four.

## Contracts

### Import contract

Caller: `cmd/cc-port`.

`cc-port import` treats every archive as a closed contract per tool. Every
placeholder token a tool's bodies contain must be accounted for before any
destination is written.

`runLocked` reads the manifest, verifies every manifest tool block and every
archive entry's tool prefix names a registered tool (`verifyManifestTools`,
`verifyEntryTools`), then preflights each present target: validates its
manifest block's categories via `manifest.ApplyToolCategories`, resolves its
implicit anchors via `Workspace.ImplicitAnchors`, merges resolutions
(`mergeResolutions`), and refuses on any declared key that is actually
referenced in a body but has no resolution (`checkMissingResolutions`). Any
mismatch aborts the import before any write. A refused import leaves the
destination untouched: no partial writes, no dangling staging temps.

Every destination each tool's `Stage` produces is a `archive.Staged{Temp,
Final}` pair; `promoteStaged` registers every one, across every tool, on a
single `rewrite.NewSafeRenamePromoter()` and promotes them as one
all-or-nothing batch via `os.Rename`. If any rename fails, every earlier
rename in the batch (across every tool, not just the one that failed) is
reversed from the saved pre-promote bytes of each replaced destination.

#### Handled

- Refused import: no write has occurred and the destination is untouched.
- Promote failure after partial rename: `SafeRenamePromoter` reverses each
  already-promoted entry, across every tool, to its pre-import state.
- Implicit anchors: each tool's `Workspace.ImplicitAnchors(TargetPath)`
  supplies that tool's own machine-local anchor keys (for example Claude's
  `{{PROJECT_PATH}}`/`{{HOME}}`/`{{PROJECT_DIR}}`, Codex's `{{CODEX_HOME}}`),
  pre-resolved to their values for this import. Caller-supplied resolutions
  for an implicit key are refused by `mergeResolutions` with
  `ImplicitKeyOverrideError`.
- A registered tool absent from the manifest: recorded in `Result.SkippedTools`,
  not treated as a hard failure.

#### Refused

These paths abort before any write:

- A tool's archive embeds a declared placeholder in at least one body whose
  key has no matching resolution. The key is absent from the merged
  resolution map and is not implicit. `MissingResolutionsError` lists every
  missing key, per tool, in alphabetical order.
- A manifest `<tool name="...">` block naming a tool this binary does not
  register at all: `manifest.UnregisteredToolError`, wrapped and returned
  before any archive entry is read.
- An archive entry whose leading path segment names a tool this binary does
  not register at all: `UnknownEntryToolError`.
- Archive entries whose names escape the staging base (containing `..`
  components or an absolute-path prefix), or whose decompressed size exceeds
  the shared caps (see [`internal/archive/README.md`](../archive/README.md)
  §Contracts): rejected by `internal/archive` before any temp file is
  created, with staged temps created so far cleaned up via `cleanupStaged`.

#### Not covered

- **Pre-fix archives with declared but unresolved keys.** Every declared key
  embedded in a body must resolve unless it is implicit. Migration: re-export
  with the current pipeline.
- **Undeclared `{{UPPER_SNAKE}}` tokens in bodies.** All `{{X}}`-shaped
  substrings a tool's manifest block does not declare are treated as
  ordinary content. Tamper detection is not the importer's responsibility.
- **What each entry actually contains and how it merges.** That is each
  tool's `Stage`/`Finalize` responsibility; see
  [`internal/tool/claude/README.md`](../tool/claude/README.md) for the
  Claude instance.

### Placeholder handling

The manifest is authoritative, per tool. Every key a tool's export path
embeds is also written into that tool's `metadata.xml` block as a
`<placeholder>` entry. `checkMissingResolutions` tests presence with
`archive.ClassifyPresentKeys`, which does a literal substring check per
candidate key.

No body grammar is parsed. The classify / stage flow records which declared
keys appear in any body of a tool's entries, then resolves them in-stream
during staging via `archive.ResolvePlaceholdersStream` or
`archive.ApplyResolutions`. `{{UPPER_SNAKE}}` substrings a tool's manifest
does not declare are content and round-trip verbatim.

`mergeResolutions` composes, per tool, three resolution sources in strength
order: the sender's own pre-filled `Resolve` values (weakest), an optional
`--from-manifest` override for that tool (stronger), and the target's
implicit anchors (strongest: cc-port computes these itself on the
destination machine, and a stale or malicious sender value must never
override them). A `--from-manifest` key the tool's archive block does not
declare is refused (`UndeclaredResolutionKeysError`), never silently ignored.
A `--from-manifest` resolution for an implicit anchor is refused
(`ImplicitKeyOverrideError`).

#### Handled

- Any declared key embedded in at least one body, with a matching
  resolution: substituted at resolve time.
- Any declared key embedded in at least one body, with no resolution, that
  is not implicit: flagged missing, that tool's import refused.
- Any declared key that does not appear in any body: ignored. A tool's
  archive block may legitimately publish metadata about keys it considered
  but did not embed.
- Any `{{UPPER_SNAKE}}` substring a tool's manifest does not declare:
  preserved byte-for-byte.

#### Refused

- Missing resolutions for declared keys (see §Import contract).
- A `--from-manifest` resolution for a key a tool's archive block does not
  declare (`UndeclaredResolutionKeysError`).
- A `--from-manifest` resolution for an implicit anchor
  (`ImplicitKeyOverrideError`).

#### Not covered

- **Hand-crafted archives that embed a key without declaring it.** Such a
  body contains a `{{KEY}}` the importer cannot resolve; the importer
  preserves the literal bytes verbatim. Tool-produced archives are not
  affected: cc-port's export path declares every key it embeds.

### Atomic staging

`cc-port import` makes every destination, across every tool, visible
all-or-nothing by having each tool's `Stage` produce sibling-temp `Staged`
records and promoting all of them together with `os.Rename` via
`promoteStaged`. `os.Rename` is atomic only within a single filesystem;
`archive.StagingTempPath` resolves the parent directory of each final
destination through any symlinks before forming the temp path, so temp and
final always share a filesystem.

`preflightStagingDirs` runs this resolution once up front for every present
target's `PreflightDirs`, before any archive byte is read. Failures across
every target are aggregated into a single error.

#### Handled

- All destinations on the same filesystem (the common macOS and Linux layout
  with everything under the home directory).
- Any destination whose parent directory is a symlink crossing a filesystem
  boundary. The temp is staged on the far side of the symlink alongside its
  final, and `os.Rename` remains intra-filesystem.
- Destinations whose parent directory does not exist yet. The resolution
  finds the closest existing prefix and `os.MkdirAll` creates the missing
  components on that filesystem.

#### Refused

These paths abort at preflight with a single aggregated error:

- A destination's symlinked parent is broken or otherwise unresolvable.
- A destination's parent ancestor walk fails with a non-not-exist stat
  error (permission denied on an intermediate component, for example).

#### Not covered

- **Filesystem topology changes mid-import.** The preflight resolves parents
  once. A concurrent operation that replaces a resolved parent with a
  cross-filesystem symlink between preflight and promote can still produce
  an `EXDEV`-class failure at rename time; the promoter rolls back and the
  import aborts, but the friendly preflight error does not fire.

Rollback is driven by `rewrite.SafeRenamePromoter`; see
`internal/rewrite/README.md` for the promoter's public API. Per-tool
staging-path construction lives in each adapter, driven by
`archive.StageSibling`.

## Tests

Unit tests in `importer_test.go`, `merge_resolutions_test.go`, and the
internal `checkmissing_internal_test.go`. Coverage:

- Basic round-trip, including a multi-tool archive importing into Claude and
  Codex targets in the same run.
- No staging temps left behind after success or after a failure.
- Refusal on unresolved declared keys and on `--from-manifest` keys a tool's
  block does not declare.
- Lookalike-token preservation: an archive whose bodies contain content
  shaped like `{{UPPER_SNAKE}}` round-trips byte-for-byte through import.
- Re-run does not duplicate history or session-index lines (import
  idempotence, verified per adapter).
- Atomic rollback on failure, across every tool's staged files.
- An unregistered manifest tool name fails hard.
- Oversized-entry and aggregate-cap rejection, enforced by `internal/archive`'s
  caps. `importer_large_test.go` (`-tags large`) exercises the real 512 MiB
  per-entry production cap; the default suite drives the same branch through
  a small test-side cap override.
- Incoming history line at/over the scanner cap.
- Progress-phase sequencing (preflight, extract, promote, finalize) and the
  extract phase counting every staged entry.

## References

- `os.Root`: local authoritative: `go doc os.Root`, online supplement: https://pkg.go.dev/os#Root
- `io.LimitReader`: local authoritative: `go doc io.LimitReader`, online supplement: https://pkg.go.dev/io#LimitReader
