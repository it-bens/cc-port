# internal/importer

## Purpose

Apply a cc-port export archive to a target path. Validates every placeholder, pre-resolves the staging filesystem topology, writes each destination through a sibling `*.cc-port-import.tmp` and promotes atomically, and refuses any archive whose body tokens do not match the manifest.

Not an exporter — the reverse direction lives in `internal/export`. Not a generic archive extractor — this module assumes the cc-port manifest + placeholder contract and rejects any ZIP that does not satisfy it.

## Public API

- **Entry point**
  - `Run(claudeHome *claude.Home, importOptions Options) error` — import an archive end-to-end: acquire lock, classify placeholders, pre-resolve staging, stage, promote, roll back on failure.
- **Placeholder classification**
  - `ClassifyPlaceholders(...)` — diff the archive's declared placeholders against the caller's resolutions and the bodies' embedded tokens; returns resolved / missing / undeclared sets.
  - `ResolvePlaceholders(content []byte, resolutions map[string]string) []byte` — substitutes every declared `{{KEY}}` in a body.
  - `ValidateResolutions(resolutions map[string]string) error` — syntactic validation of caller-supplied resolutions.
- **Conflict check**
  - `CheckConflict(encodedProjectDir string) error` — refuses the import if the encoded target directory already exists.
- **Types**
  - `Options` — import configuration: `ArchivePath`, `TargetPath`, `Resolutions`. The struct also carries an unexported `renameHook` used by tests.

## Contracts

### Import contract

`cc-port import` treats every archive as a closed contract: every
placeholder token a body contains must be accounted for before any
destination is written. The pre-flight gate in
`internal/importer/importer.go:Run` scans every ZIP entry, diffs against
the manifest's declared placeholders and the caller's resolutions, and
refuses the import on any mismatch. The rollback surface (see below)
means a refused import leaves the destination untouched — no partial
writes, no dangling staging temps.

Atomicity — every destination is staged at a sibling
`*.cc-port-import.tmp` path and promoted via `os.Rename`:

- `<encoded-project-dir>.cc-port-import.tmp` → `<encoded-project-dir>`
- `~/.claude/history.jsonl.cc-port-import.tmp` → `~/.claude/history.jsonl`
- `~/.claude.json.cc-port-import.tmp` → `~/.claude.json`
- per-entry file-history temps → their final `~/.claude/file-history/…`
  destinations

`internal/rewrite/rewrite.go:SafeRenamePromoter` drives the promote and
owns the rollback: if any rename step fails, every earlier rename is
reversed from the saved pre-promote bytes of each replaced destination.

Refused by cc-port — these paths abort before any write:

- Archive declares a placeholder marked `Resolvable: true` (or
  unspecified) whose key is not in `Options.Resolutions` and is not
  cc-port's implicit `{{PROJECT_PATH}}`. The error lists every missing
  key in alphabetical order.
- Archive body contains a `{{KEY}}` that the manifest does not declare
  at all. The error lists every undeclared key in alphabetical order.

Allowed-to-remain-symbolic — a placeholder marked `Resolvable: false` in
the manifest stays verbatim on disk even if no resolution was supplied.
This is the explicit escape hatch for "the sender acknowledges this
path has no meaning on the recipient's machine".

Not covered — cases cc-port does not address:

- **Pre-refactor archives with implicit unresolved keys.** Archives
  written by older cc-port versions whose manifest declared
  `{{KEY}}` (with `Resolvable: nil`, now meaning "must be resolved")
  without the caller supplying `{{KEY}}` are now refused. Migration:
  supply the resolution, or re-export with the key marked
  `Resolvable: false`.
- **Undeclared exotic token shapes in bodies.** See
  _Placeholder resolution scope_ above — the tamper-defense scan is
  grammar-bounded and does not catch lowercase, punctuated, or
  whitespace-bearing tokens in bodies that the manifest fails to
  declare. Resolution of declared keys is unaffected.

### Placeholder resolution

The import pre-flight gate in
`internal/importer/resolve.go:ClassifyPlaceholders` decides which keys
the archive embeds, which of those still need a resolution, and which
tokens the archive embeds without having declared them. A scanner that
parsed placeholder tokens directly out of body bytes would have to
commit to a grammar — and any grammar narrow enough to avoid false
positives on ordinary JSON or Markdown `{{…}}` content would be too
narrow to catch exotic keys a future exporter might emit.

The manifest is authoritative instead. Every key cc-port's export path
embeds is also written into `metadata.xml` as a `<placeholder>` entry,
so the importer iterates the declared set and decides presence with a
literal `bytes.Contains` per key. No body grammar is parsed on the
resolution path; the exporter's key shape, whatever it is, is correctly
classified by construction. `internal/rewrite/rewrite.go:FindPlaceholderTokens`
is retained only as a tamper-defense scan: upper-snake `{{KEY}}` tokens
in bodies that are absent from the manifest are reported as undeclared,
so an archive whose bodies and manifest disagree is refused before any
write.

Correctly classified — by construction, regardless of key shape:

- Any declared key embedded in at least one body, with a matching
  resolution: substituted at resolve time.
- Any declared key embedded in at least one body, with no resolution
  and `Resolvable` unset or `true`: flagged `missing`, archive refused.
- Any declared key marked `Resolvable: false`: allowed to survive on
  disk verbatim, even when no resolution is supplied.
- Any declared key that does not appear in any body: ignored. The
  archive may legitimately publish metadata about keys it considered
  but did not embed.
- `{{PROJECT_PATH}}`: resolved implicitly by `importer.Run` from the
  import target path, so it is treated as resolved even when absent
  from the caller's resolution map.

Caught by the tamper-defense scan — upper-snake shape only:

- An `{{UPPER_SNAKE}}` token embedded in a body that the manifest does
  not declare: reported as `undeclared`, archive refused.

Residual risk — cases this design does not cover:

- **Undeclared exotic-shape tokens in bodies.** A hand-crafted or
  tampered archive whose body contains a lowercase, punctuated, or
  whitespace-bearing token (e.g. `{{my-weird.key}}`) that is not
  declared in `metadata.xml` is invisible to the tamper-defense scan.
  The token would neither be flagged as undeclared nor substituted at
  resolution time, and would survive verbatim on disk. Widening the
  scanner's grammar to catch these would produce false positives on
  legitimate `{{…}}` content embedded in transcripts (Handlebars,
  Mustache, Jinja). Tool-produced archives are not affected because
  cc-port's export path publishes every key it embeds; hand-crafted
  archives that want the full contract must list every embedded key
  in the manifest.

The tamper-defense scan that catches undeclared `{{UPPER_SNAKE}}` tokens lives in `rewrite.FindPlaceholderTokens` — see `internal/rewrite/README.md` §Boundary rules for the surrounding rewrite primitive.

### Atomic staging

`cc-port import` makes every destination visible all-or-nothing by
staging each write at a sibling `*.cc-port-import.tmp` path and
promoting it with `os.Rename`. `os.Rename` is atomic only within a
single filesystem, and a bare-sibling temp path would sit on the
wrong side of the boundary whenever a destination's parent is a
symlink to another volume (a common layout for
`~/.claude/file-history` pointed at an external disk), so the
promote step would fail mid-import with `EXDEV`.

`internal/importer/importer.go:stagingTempPath` resolves the parent
directory of each final destination through any symlinks before
forming the temp path, so temp and final are siblings of the
*resolved* parent and therefore always share a filesystem. The walk
handles missing trailing components the same way
`internal/claude/paths.go:ResolveProjectPath` handles nonexistent
project paths: the longest existing prefix is symlink-resolved, and
any missing tail is re-attached unchanged so `MkdirAll` creates it on
the resolved filesystem at stage time.

`internal/importer/importer.go:checkStagingFilesystems` runs this
resolution once up front for every destination the importer will
touch (the encoded project directory, `history.jsonl`,
`.claude.json`, and the file-history base) and aggregates any
failures into a single error before the archive is read or any temp
is written. This turns an obscure mid-promote rename failure into a
clear "resolve staging parent for X" message that fires before the
import has touched anything.

Handled — layouts where promotion stays atomic:

- All four destinations on the same filesystem (the common macOS and
  Linux layout with everything under the home directory).
- Any subset of destinations whose *parent directory* is a symlink
  crossing a filesystem boundary (e.g. `~/.claude/file-history`
  pointed at an external volume). The temp is staged on the external
  volume alongside its final, and `os.Rename` remains intra-filesystem.
- Destinations whose parent directory does not exist yet. The
  ancestor walk finds the closest existing prefix, resolves it, and
  `MkdirAll` creates the missing components on that filesystem.

Refused before any write — these paths abort at preflight with a
single aggregated error:

- A destination's symlinked parent is broken or otherwise
  unresolvable (`EvalSymlinks` returns a non-`ENOENT` error).
- A destination's parent ancestor walk fails with a non-`ENOENT`
  stat error (permission denied on an intermediate component, etc.).

Not covered — cases this approach deliberately does not address:

- **Final destination is itself a cross-filesystem symlink.** If
  `~/.claude/projects/<encoded>`, `~/.claude/history.jsonl`, or
  `~/.claude.json` already exists as a symlink whose target lives on
  a different filesystem than the symlink's parent,
  `CheckConflict`/merge refuses or overwrites based on existing-file
  rules, not on symlink topology. For the project directory
  specifically, `CheckConflict` refuses when the encoded directory
  already exists, so a pre-existing symlinked leaf does not reach
  the rename. A symlinked `history.jsonl` or `.claude.json` leaf
  would still route through `os.Rename` on the symlink's parent
  filesystem; if the symlink itself straddles a boundary the
  promote fails and the rollback surface (see **Import contract
  scope**) restores pre-import state.
- **Filesystem topology changes mid-import.** The preflight resolves
  parents once. A concurrent operation that replaces a resolved
  parent with a cross-filesystem symlink between preflight and
  promote can still produce `EXDEV` at rename time; the promoter
  rolls back and the import aborts, but the friendly preflight
  error does not fire.

The rollback surface is driven by `SafeRenamePromoter` — see `internal/rewrite/README.md` §Boundary rules for the promoter's public API; the import flow itself owns the staging temp-path resolution in `internal/importer/importer.go:stagingTempPath`.

### File-history handling (import)

File-history snapshots are opaque byte streams; see root README §File-history policy for the cross-cutting framing that governs every command.

Handled — `cc-port import` writes snapshots back to disk as the opaque bytes the archive carried. `ResolvePlaceholders` still runs over every entry for compatibility with older archives (a `{{KEY}}` that somehow survived inside a body will still be substituted), but on snapshots produced by current cc-port the pass is a no-op because no tokens are present.

## Tests

Unit tests in `importer_test.go` and `resolve_test.go`. Coverage: basic round-trip, no staging temps left behind, refusal on unresolved / undeclared keys, acceptance of `Resolvable: false`, atomic rollback on failure, conflict refusal on pre-existing encoded directories.
