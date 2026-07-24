# internal/tool

## Purpose

Defines the compile-time contract every supported coding tool implements, plus
the shared registry and rollback primitives every command package drives that
contract through. `internal/tool/claude` and `internal/tool/codex` implement
it; `internal/move`, `internal/export`, `internal/importer`, and
`internal/stats` consume it and import nothing else tool-specific. See
`docs/architecture.md` §The tool contract for the cross-module narrative this
package's types support.

## Public API

- **Tool contract**
  - `Tool`: `Name`, `DisplayName`, `Categories`, `Detect`, `Open`,
    `ImplicitAnchorKeys`. The tool-level, connection-free half of the
    contract.
  - `CategoryNames(t Tool) []string`: the wire names of `t`'s declared
    categories in canonical order.
  - `Workspace`: composes `Mover`, `Exporter`, `Importer`, `Auditor` plus
    `Root`, `LockPath`, `ActiveWriters`. One `Tool.Open` result serves every
    command.
  - `Mover`: `MoveSurfaces(MoveRequest) ([]Surface, error)`,
    `ResidualWarnings(MoveRequest) ([]string, error)`.
  - `Exporter`: `Placeholders(project string, selected map[string]bool) ([]manifest.Placeholder, error)`,
    `Export(ctx, project string, selected map[string]bool, sink *archive.Sink) (ExportResult, error)`.
  - `Importer`: `PreflightDirs(project string) []string`,
    `ImplicitAnchors(project string) (map[string]string, error)`,
    `Stage(ctx, project string, entry archive.Entry, resolutions map[string]string) ([]archive.Staged, error)`,
    `Finalize(ctx, project string, staged *archive.StagedSet) ([]string, error)`.
  - `Auditor`: `ReferenceSurfaces(ctx context.Context, project string) ([]CountSurface, error)`,
    `DiskCategories(ctx context.Context, project string) ([]SizeCategory, error)`,
    `EnumerateProjects(ctx context.Context) ([]ProjectInfo, error)`.
- **Contract value types**
  - `Category`: `Name`, `Description`, `DefaultSelected`, `ExcludedFromAll`
    (kept out of the `--all` sweep and exported only via explicit
    `--include`, picker selection, or a manifest that marks it included).
  - `Qualified`: `Tool`, `Category` (one `<tool>/<category>` pair).
  - `MoveRequest`: `OldPath`, `NewPath`, `RefsOnly`, `DeepRewrite`.
  - `ActiveWriter`: `Pid`, `Cwd` (one piece of liveness evidence).
  - `Surface`: `Name`, `Plan func(ctx) (SurfaceResult, error)`,
    `Apply func(ctx, *Restorer) (SurfaceResult, error)`. One named,
    independently plannable and applicable unit of a move.
  - `SurfaceResult`: `Count`, `Warnings`. A surface returns warnings it
    discovers while planning or applying. `ResidualWarnings` returns
    request-level residuals that require no surface execution to discover.
  - `ArchiveEntry`, `ExportResult` (`Categories`, `Skipped`, `Warnings`),
    `CountSurface`, `SizeCategory`, `ProjectInfo` (`Label`, `Resolved`,
    `Disk`, `Files`, `Bytes`).
  - Sentinel errors `ErrToolAbsent`, `ErrProjectAbsent`, `ErrNoWitness` (see
    `docs/architecture.md` §Sweep semantics).
- **Registry**
  - `Set`: the registered collection of tools, built once via `NewSet` and
    read thereafter through `All`, `ByName`, `Detected`.
  - `Target`: `Tool` paired with its already-opened `Workspace`.
  - `NewSet(tools ...Tool) *Set`: rejects an empty registry, validates unique names, unique qualified
    categories, and unique implicit placeholder keys across every tool;
    panics on a violation.
  - `ParseQualified(raw string) (Qualified, error)`: parses a `"<tool>/<category>"`
    argument; a bare category name with no slash is refused.
- **Rollback**
  - `Restorer`: collects rollback state for one `Surface.Apply` pass.
  - `NewRestorer() *Restorer`
  - `(*Restorer).RegisterFile(path string) error`: snapshots `path`'s current
    contents before a caller overwrites it in place.
  - `(*Restorer).RegisterUndo(fn func() error)`: records a non-file rollback
    (a SQL transaction rollback, for example).
  - `(*Restorer).Restore() error`: reverses every registration in reverse
    order, joining errors.
  - `(*Restorer).Cleanup()`: discards sibling backup files once the caller's
    operation has fully succeeded.
- **Path resolution**
  - `ResolveProjectPath(path string) (string, error)`: normalizes a
    user-supplied project path (expands a leading `~/`, makes it absolute,
    resolves it through existing symlinked ancestors).

`ImplicitAnchorKeys`, `Exporter.Export`'s `*archive.Sink` parameter, the
fallible `ImplicitAnchors`, and `Finalize`'s `[]string` warnings return are
each an accepted deviation from spec §1.

## Contracts

### Contract surface

**Handled.**

- The adapter-import boundary and the sweep-sentinel interpretation are
  cross-module policies owned by `docs/architecture.md` §Adapter boundaries
  and §Sweep semantics; `NewSet` fixes the tool list once, at process
  construction, and every consumer of `ErrToolAbsent`/`ErrProjectAbsent`/`ErrNoWitness`
  follows those two sections rather than a local rule.

**Refused.**

- An empty registry, an empty tool name, duplicate tool names, duplicate `(tool, category)`
  pairs, or two tools claiming the same implicit placeholder key: `NewSet`
  panics rather than silently picking one. These are registry-construction
  bugs caught at process start, not operational errors a caller recovers
  from.
- A bare `--include` category name with no `<tool>/` prefix: `ParseQualified`
  refuses it, since multi-tool selection requires the tool segment.

**Not covered.**

- Discovering tools at runtime (plugins, config-driven registration). The
  registry is a fixed, compiled-in list; adding a tool means adding a line in
  `cmd/cc-port/tools.go` and rebuilding.

### Extension rule

A third adapter is one new package (`internal/tool/<name>`) plus one line in
`cmd/cc-port/tools.go`; no command package and no existing adapter changes.

**Handled.**

- The new package implements the full `Tool` and `Workspace` surface: home
  resolution under the tool's own rules, `Categories()`, an
  adapter-internal identity witness only when the tool's own naming is lossy
  (Claude needs one for its encoded-directory collisions; Codex does not,
  since it stores `cwd` verbatim), `MoveSurfaces` built from the growable
  substrate primitives (`internal/rewrite`, `internal/sqlrewrite`),
  `Placeholders`/`Export` with any tool-declared home anchors,
  `PreflightDirs`/`ImplicitAnchors`/`Stage`/a deduplicating `Finalize`, the
  three `Auditor` methods, `ActiveWriters`, and round-trip fixtures.
- Everything else is inherited unchanged: CLI wiring and generated flags
  (`cmd/cc-port/toolselect.go`), locking (`internal/lock`), archive
  mechanics and caps (`internal/archive`), manifest I/O and validation
  (`internal/manifest`), encryption, remote sync, progress, scan warnings,
  and every existing substrate primitive.
- `internal/rewrite` and `internal/sqlrewrite` are growable substrate, not
  closed sets: a new tool whose storage fits an existing engine (raw bytes,
  JSONL, SQL, TOML) inherits it unchanged; a new tool needing a
  genuinely new operation on an existing substrate adds that operation to
  the primitive, reusing its existing safety envelope, rather than opening
  an ad-hoc connection outside it.

**Refused.**

- A wholly new storage engine for a tool whose state fits none of the five
  substrate shapes above is the one case that requires new substrate, not
  merely a new operation on existing substrate.
- Any change to `internal/manifest`, `internal/archive`, or `internal/lock`
  as part of adding a third adapter. Those packages are tool-agnostic by
  construction; if adding a tool seems to require changing one of them, the
  tool's shape has revealed a gap in the contract itself, not a one-off
  extension.

**Not covered.**

- Migrating an existing archive format when a new adapter changes the
  manifest's per-tool block shape. The manifest schema is fixed (see
  `internal/manifest/README.md` §Category manifest); a new adapter uses the
  existing `Tool`/`Category`/`Placeholder` shapes.

### Restorer semantics

**Handled.**

- `RegisterFile` snapshots files under 1 MiB as an in-memory byte copy;
  larger files stream to a sibling backup file first, so restoring a large
  rewritten file does not hold the whole original in RAM.
- `Restore` walks registrations in reverse order (the most recent change
  undone first) and joins every restoration error via `errors.Join` rather
  than stopping at the first failure, so a caller sees every surface that
  could not be rolled back.
- `RegisterUndo` gives non-file surfaces (a SQL transaction) the same
  reverse-order rollback sequence as file registrations, interleaved by
  registration order regardless of which registration method was used.

**Refused.**

- Restoring after `Cleanup` has already run. `Cleanup` is a one-way
  discard of the sibling backup files `RegisterFile` created; callers invoke
  it only once their operation has fully succeeded and no `Restore` will
  follow.

**Not covered.**

- Ordering guarantees across two independent `Restorer` instances (one per
  tool). Cross-tool rollback does not exist by design; see
  `docs/architecture.md` §No cross-tool rollback.

## Tests

Unit tests in `path_test.go`, `restorer_test.go`, `set_test.go`. Coverage:
`ResolveProjectPath` tilde expansion and symlink resolution, `Restorer`'s
in-memory vs. sibling-backup threshold and reverse-order restore including a
mixed file-and-undo registration sequence, and `NewSet`'s empty-registry,
empty-name, duplicate-name, duplicate-qualified-category, and duplicate-key
panic conditions alongside its
`ByName`/`Detected` accessors and the package-level `ParseQualified` parser.
