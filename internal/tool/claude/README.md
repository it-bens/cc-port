# internal/tool/claude

## Purpose

Models Claude Code's on-disk data layout. Encodes project paths into the
`~/.claude/projects/<encoded>/` directory name and enumerates the files
that belong to one project.

Not a rewriting package. The module produces locations and types.
`internal/move`, `internal/export`, and `internal/importer` drive the mutations.

## Public API

- **Path encoding**
  - `EncodePath(absPath string) string`: mirror of Claude Code's lossy
    encoding (`/`, `.`, space collapse to `-`, with a leading `-`).
- **Home**
  - `NewHome(override string) (*Home, error)`: constructs a `~/.claude`
    root, honouring the generated `--claude-home` flag override.
  - `Home`: struct with `Dir` and `ConfigFile` fields plus path-deriving
    methods `ProjectsDir`, `ProjectDir`, `HistoryFile`, `SessionsDir`,
    `SettingsFile`, `RulesDir`, `FileHistoryDir`, `TodosDir`,
    `UsageDataDir`, `PluginsDataDir`, `TasksDir`, `PluginsInstalledFile`,
    `KnownMarketplacesFile`.
- **Project enumeration**
  - `LocateProject(claudeHome *Home, projectPath string) (*ProjectLocations, error)`:
    returns every file tied to a project. Errors if the project directory
    does not exist. Optional resources are zero-valued when absent.
  - `ProjectLocations`: struct holding the set.
  - `EnumerateProjects(claudeHome *Home) ([]ProjectEnumeration, error)`:
    lists every encoded project directory with the data needed to size its
    disk footprint. An absent or empty projects directory yields an empty
    slice, not an error.
  - `ProjectEnumeration`: struct with `EncodedName`, `ResolvedPath` (the
    witness-resolved real path, empty when no witness exists), and
    `Locations`.
  - `TranscriptFiles(ctx context.Context, projectDir string) ([]string, error)`:
    a project dir's transcript body files (top-level `*.jsonl` plus every file
    under each subdirectory other than `memory`/`sessions`). `move` rewrites
    this set; `stats` counts and sizes it.
- **Session-keyed registry**
  - `RegistryEntry`: descriptor struct for Claude Code storage surfaces.
  - `Registries`: ordered source of truth for all storage surfaces.
  - `SessionKeyedGroups() iter.Seq[RegistryEntry]`: yields session-keyed
    entries in registry order.
  - `(*ProjectLocations).AllFlatFiles() iter.Seq2[RegistryEntry, string]`:
    yields `(group, absolute path)` pairs in registry order, applying
    each group's `SidecarFilter`. Performs no I/O and supports early
    termination via `break`.
- **User-wide registry**
  - `UserWideRewriteTargets() iter.Seq[RegistryEntry]`: yields user-wide
    rewrite entries in registry order. `internal/move` consumes them;
    `internal/export` and
    `internal/importer` intentionally do not iterate it (these files are
    machine-local and do not belong in a cross-machine archive).
- **Schemas**
  - `HistoryEntry`, `SessionFile`, `UserConfig`, `SettingsMarketplace`,
    `SettingsMarketplaceSource`: Go types for the JSON/JSONL files
    cc-port reads and writes. `HistoryEntry`, `SessionFile`, and
    `UserConfig` implement `json.Marshaler` and `json.Unmarshaler`,
    preserving unknown fields in an `Extra` map.
  - `MaxHistoryLine`: 16 MiB ceiling for a single `history.jsonl` line
    read through `bufio.Scanner`. Scoped to this adapter: every scanner in
    this package that reads Claude's `history.jsonl` shares it. Codex's own
    `history.jsonl` is capped separately, by its own `maxCodexJSONLLine`
    constant at the same 16 MiB value.
- **Tool contract implementation** (`Adapter`, `Workspace` in `adapter.go`)
  - `(*Workspace).MoveSurfaces(tool.MoveRequest) ([]tool.Surface, error)`,
    `(*Workspace).ResidualWarnings(tool.MoveRequest) ([]string, error)`
    (`move.go`): the ordered per-surface rewrite this adapter performs for
    `cc-port move`.
  - `(*Workspace).Placeholders(project string, selected map[string]bool) ([]manifest.Placeholder, error)`,
    `(*Workspace).Export(ctx, project string, selected map[string]bool, sink *archive.Sink) (tool.ExportResult, error)`
    (`export.go`, `discover.go`): placeholder discovery and category export
    for `cc-port export`.
  - `(*Workspace).PreflightDirs`, `(*Workspace).ImplicitAnchors`,
    `(*Workspace).Stage`, `(*Workspace).Finalize` (`import.go`): archive
    staging and merge for `cc-port import`.
  - `(*Workspace).ReferenceSurfaces`, `(*Workspace).DiskCategories`,
    `(*Workspace).EnumerateProjects` (`stats.go`): read-only footprint
    accounting for `cc-port stats`.

## Contracts

### Path encoding

cc-port identifies every project by its encoded directory name under
`~/.claude/projects/`. The encoding is inherited from Claude Code. The
input path is resolved through the filesystem following symlinks, then `/`,
`.`, and space each map to `-`, and a leading `-` is prepended.

The encoding is lossy. Distinct paths collapse to the same name:

- `/Users/x/Projects/my project`
- `/Users/x/Projects/my-project`
- `/Users/x/Projects/my.project`

All encode to `-Users-x-Projects-my-project`. cc-port applies the same
encoding and symlink resolution to user-supplied paths. The encoded name must
match what Claude Code wrote on disk. The original path cannot be recovered
from the encoded form.

`checkEncodedDirCollision` (`move.go`) enforces the encoded-name safety check
for `cc-port move`: it aborts before any write when old and new encode
identically, and refuses a target encoded directory that already exists,
carries no valid promotion marker, and whose old source is still present
(see §Apply contract (move)).

`cc-port import` does not refuse on an existing encoded directory. Promotion
overwrites it in place: `rewrite.SafeRenamePromoter` backs up any displaced
content (in-memory bytes for a file, a sibling stash for a directory) before
renaming the staged temp over it, so a promote failure still rolls back to
the pre-import state. A re-run of the same import converges rather than
refusing (see [`internal/importer/README.md`](../../importer/README.md)
§Atomic staging).

#### Handled

- Encoding input paths that contain `/`, `.`, or space: each is mapped to
  `-`. Paths that begin with `/` gain a leading `-`.
- `tool.ResolveProjectPath` resolves user input before encoding, so the result
  matches what Claude Code wrote.

#### Refused

- `cc-port move` (apply or dry-run) where old and new paths encode to the
  same directory name. The copy-and-delete sequence cannot run against a
  single on-disk location. Proceeding would destroy data.
- `cc-port move` (apply or dry-run) where the target encoded directory
  already exists. Another project path has claimed that storage. Proceeding
  would silently merge or overwrite its data.

#### Not covered

- **Pre-existing collisions with no session witness.** If two distinct
  paths were already stored in the same encoded directory and neither
  has a session JSON that would witness the true owner, the data is
  interleaved and `verifyProjectIdentity` cannot detect it. See
  `§Project enumeration` for how the guard behaves when at least one
  witness exists.
- **Decoding a directory name back to a path.** One encoded name maps to
  any of several real paths. cc-port never decodes. Every operation takes
  the original path as input and encodes forward. To find the owner of a
  stored directory, read `cwd` from a `sessions/*.json` file or the
  matching `~/.claude.json` project key.
- **Forward re-derivation for the encoded-dir placeholder.** Export, import,
  and move now rewrite the encoded `~/.claude/projects/<encoded>` reference
  inside session-subdir bodies via `{{PROJECT_DIR}}` (export/import) and a
  second `ReplacePathInBytes` pass (move). This re-derives the encoded form
  forward from a known real path (`ProjectDir`); it never decodes an encoded
  name back to a path.
- **A pre-existing encoded directory at import time.** Unlike move, import
  does not check for or refuse this case; see §Stage and Finalize (import).

### Project enumeration

`LocateProject` returns a `ProjectLocations` struct whose fields cover
every file and directory tied to the project:

- `ProjectDir`: the encoded `~/.claude/projects/<encoded>/` path.
- `HistoryEntryCount int`: count of `~/.claude/history.jsonl` lines whose
  `project` field equals the project path.
- `SessionFiles`: `~/.claude/sessions/*.json` entries whose JSON `cwd`
  matches the project path (the sessions directory is user-wide).
- `MemoryFiles`: `~/.claude/projects/<encoded>/memory/` subtree.
- `SessionTranscripts`: `~/.claude/projects/<encoded>/*.jsonl` transcripts.
- `SessionSubdirs`: `~/.claude/projects/<encoded>/<session-uuid>/`
  per-session subdirectories under the project dir.
- `FileHistoryDirs`: `~/.claude/file-history/<session-uuid>/` directories
  whose UUID is in the project's session set.
- `HasConfigBlock bool`: true when `~/.claude.json` contains a `projects`
  entry keyed by this project path.
- `TodoFiles`: `~/.claude/todos/<sid1>-agent-<sid2>.json` where either UUID
  is in the project's session set. The filename admits sub-agent spawns.
  Both parent and child session UUIDs receive independent visibility.
- `UsageDataSessionMeta`: `~/.claude/usage-data/session-meta/<sid>.json`
  for each `<sid>` in session set.
- `UsageDataFacets`: `~/.claude/usage-data/facets/<sid>.json` for each
  `<sid>` in session set.
- `PluginsDataFiles`: `~/.claude/plugins/data/<ns>/<sid>/` subtrees where
  `<sid>` is in session set. Plugin namespace `<ns>` is opaque.
- `TaskFiles`: `~/.claude/tasks/<sid>/` for each `<sid>` in session set.
  `.lock` and `.highwatermark` sidecars appear in `TaskFiles` but
  `AllFlatFiles()` skips them via the registry's `SidecarFilter`.

#### Handled

- Each session-keyed collector returns empty when its parent directory is
  absent, matching the behaviour of `collectMemoryFiles`. The directory may
  not exist if the feature has never been used.
- After the project directory is confirmed to exist, `verifyProjectIdentity`
  walks `~/.claude/sessions/*.json` and collects the distinct `cwd` values
  reported by sessions whose `sessionId` matches a UUID inside the encoded
  project directory. A `cwd` equal to `projectPath` confirms identity.

#### Refused

- `LocateProject` fails when the project directory does not exist.
- `LocateProject` also fails when the encoded directory has at least one
  session witness and none of them report a matching `cwd`. Two distinct
  real paths can encode to the same directory name (`my project`,
  `my-project`, and `my.project` all encode the same); without this guard
  a rewrite would splice one project's data into another's. The error
  names the encoded directory and the requested project path.

#### Not covered

- Callers that need an exhaustive walk use `AllFlatFiles()`, which applies
  the sidecar filter automatically.
- Projects with no session witnesses (no sessions attributed yet, or the
  sessions directory absent) cannot be identity-verified. `LocateProject`
  logs a one-line warning to `os.Stderr` and proceeds so fresh projects
  still work.

### All-projects enumeration

`EnumerateProjects` lists every directory under `ProjectsDir()`, skipping
non-directory entries, and per directory resolves a real-path label from a
session witness and collects the owned-data locations used for disk sizing. It
is the all-projects counterpart to `LocateProject`.

Unlike `LocateProject` it takes no caller-supplied path and runs no identity
cross-check: with no requested path there is nothing to contradict. The real
path is recovered from a session witness (see §Path encoding) when one exists
and is left empty otherwise. `sessions/*.json` are attributed by `cwd`, so a
witness-less directory contributes no session files; the session-UUID-keyed
categories are collected regardless.

#### Handled

- A witness resolves the lossy encoding to the real path the session reported,
  recovering which of several colliding paths owns the directory.
- A witness-less directory still reports disk metrics; only the label degrades
  to the encoded name.

#### Refused

- Nothing per project. A missing projects directory is an empty result, not an
  error.

#### Not covered

- Reference counts. An arbitrary encoded directory has no confirmed real path
  to scan shared files against, so references stay a single-project concern.
- The shared global `history.jsonl` and `~/.claude.json` carry no per-project
  disk footprint and are not consulted here.

### Session-keyed registry

`Registries` is the canonical registry for session-keyed and user-wide surfaces.
Downstream consumers iterate it rather than open-coding group names. Add a
session-keyed directory as one row with a non-nil `Files` field.

Each group's `Category` field names one of this adapter's own declared
category names (`claude.New().Categories()`, see `categories.go`) that gates
its export. The two `usage-data/*` groups both carry `"usage-data"`, so a
single category flag covers both subgroups. `TestSessionKeyedGroups_CategoriesAreDeclaredCategories`
in `session_keyed_groups_drift_test.go` fails when a group ships with a
Category outside that declared set.

#### Handled

- Registry iteration via `AllFlatFiles()` applies each group's
  `SidecarFilter`. The only current filter is `isTaskSidecar`, which
  matches `.lock` and `.highwatermark` under `tasks/`.
- Slice order defines the display and iteration order downstream. Every
  consumer that needs to walk session-keyed files iterates `AllFlatFiles()`
  rather than open-coding each group.

#### Refused

- None at runtime. The registry is a package-level var. Callers read it
  and do not add to it at runtime.

#### Not covered

- None inherent to the registry. Extension (adding a group) is a
  compile-time edit, not a runtime concern.

### User-wide registry

`UserWideRewriteTargets` yields the user-wide files whose bytes may contain
references to a project path and can be rewritten by component-boundary-aware
byte replacement. Current entries: `settings` (`~/.claude/settings.json`),
`plugins/installed_plugins` (`~/.claude/plugins/installed_plugins.json`),
`plugins/known_marketplaces` (`~/.claude/plugins/known_marketplaces.json`).

Files with structurally distinct rewriters stay outside the registry:
`history.jsonl` (JSONL streaming), session files under
`~/.claude/sessions/*.json` (JSON round-trip via `RewriteSessionFile`), and
`~/.claude.json` (JSON round-trip via `RewriteUserConfig`). Forcing them in
would require a strategy field on every entry.

Adding a user-wide file means one `Registries` row and one `Home`
path-derivation method on `home.go`.

#### Handled

- Registry iteration in `internal/move` walks `UserWideRewriteTargets()` once
  in `rewriteUserWideFiles` (Apply) and once in `countUserWideReplacements`
  (DryRun). Both use the same slice order.
- Missing target files contribute zero to DryRun counts and are skipped at
  Apply (matching the existing settings-absent behavior).

#### Refused

- None at runtime. `Registries` is a package-level var. Callers read it and
  do not add to it at runtime.

#### Not covered

- `internal/export` and `internal/importer` do not consume the registry.
  Plugin-registry files are machine-local and stay out of archives.

### Schema types

Go types `HistoryEntry`, `SessionFile`, `UserConfig`, `SettingsMarketplace`,
and `SettingsMarketplaceSource` model the JSON/JSONL files cc-port reads and
writes. Each type with an `Extra` field uses a custom `UnmarshalJSON` and
`MarshalJSON` pair to preserve unknown fields across a rewrite cycle.

#### Handled

- Unknown JSON fields are preserved in `Extra map[string]json.RawMessage`
  and round-tripped by `MarshalJSON`. Claude Code may add fields in future
  versions without breaking a round-trip through cc-port.
- `HistoryEntry` carries `project`, `SessionFile` carries `cwd` and `pid`,
  and `UserConfig` carries `projects`. All other fields pass through `Extra`.

#### Refused

- None at runtime. Malformed JSON returns an error from `UnmarshalJSON`.
  The package never silently discards a failed unmarshal.

#### Not covered

- Schema validation beyond field presence. Callers that need value
  constraints must enforce them after unmarshaling.

### Witness liveness

`Workspace.ActiveWriters` reads session files and resolves each named PID
through the workspace's injected liveness seam.

#### Handled

- A live PID produces its session's `Cwd` and PID as an active writer; a dead
  PID produces no active writer.

#### Refused

- An unreadable-but-byte-present session file, including malformed JSON,
  returns an error wrapping `tool.ErrNoWitness` rather than silently skipping
  the evidence.

#### Not covered

- Process-table scanning. Claude checks only PIDs named in session files.

### History line cap

The exported constant `MaxHistoryLine` (16 MiB) caps a single
`history.jsonl` line. The file's streaming line readers enforce it through one
of two mechanisms, so an oversized line fails loudly instead of being
truncated. A new streaming `history.jsonl` reader must keep the cap so the
observable limit stays consistent across commands.

#### Handled

- `bufio.Scanner` readers call `scanner.Buffer(make([]byte, 64<<10),
  MaxHistoryLine)`: `countHistoryEntries` here, `move.scanHistoryFile`, and
  `stats.countHistoryReferences`. The buffer starts at 64 KiB and grows only
  up to the cap.
- `bufio.Reader` readers wrap `bufio.NewReaderSize(src, 64<<10)`, read each
  line with `ReadBytes('\n')`, and reject any line longer than
  `MaxHistoryLine`: `StreamHistoryJSONL` (move's rewrite path) and
  `export.writeJSONLToZip` (the export path).
- Both mechanisms report an oversized line as `bufio.ErrTooLong`, wrapped
  with `%w` so a caller reaches it through `errors.Is`.

#### Refused

- Silent truncation of an oversized line. The read fails and the command
  surfaces the error.

#### Not covered

- `internal/importer` reads the existing `history.jsonl` whole through
  `os.ReadFile` when merging imported entries, so it is bounded by available
  memory, not by `MaxHistoryLine`.
- The `internal/scan` package's own 16 MiB cap on rules files. Same number,
  different content domain. The two caps are coincident and independent.

### Apply contract (move)

`MoveSurfaces` returns one `tool.Surface` per rewrite unit, in a load-bearing
order: history, user-wide files, session-keyed groups, config, transcripts
(only under `--deep`), memory, sessions, then `project-directory` last of
all. Session-keyed groups, transcripts, memory, and sessions re-verify
project identity via `LocateProject`'s session witness on each call.
History, user-wide files (§User-wide registry), and config are home-wide
rather than per-project, so there is no project identity to re-verify; each
scans or rewrites its one file directly. The `sessions` surface is the one
that rewrites the witness's `cwd` field, so it must run after every other
per-project reference rewrite or a later identity check would see the
witness already pointing at the new path. `project-directory` runs last
because it derives paths directly from `Home.ProjectDir` and never calls
`LocateProject`.

#### Handled

- Every plain-bytes surface with substitutable content (user-wide,
  session-keyed) routes through `rewriteTracked`: `Restorer.RegisterFile`,
  then `rewrite.ReplacePathInBytes`, then `rewrite.SafeWriteFile`. History
  and config are format-aware instead: `historySurface` streams through
  `StreamHistoryJSONL`, and `configSurface` rewrites through
  `RewriteUserConfig`.
- Transcripts and memory route through `rewriteTwicePreservingMtime`, which
  additionally rewrites the encoded storage-directory form
  (`Home.ProjectDir(oldPath)` to `Home.ProjectDir(newPath)`) in the same
  pass and restores the file's pre-rewrite mtime afterward.
- `project-directory` copies the encoded storage directory (and, unless
  `RefsOnly`, the on-disk project directory) to the new path via
  `rewrite.PromoteDir` with `fsutil.CopyDir`, verifies both copies landed,
  then removes the originals.
- `checkEncodedDirCollision` runs before any surface: a move where old and
  new paths encode to the same directory (`ErrEncodedDirAmbiguous`) is
  refused before any write. A plan reports an existing
  `.cc-port-staging.tmp` sibling and the project-directory surface reconciles
  it during locked Apply. A target that has already converged, with a valid
  promotion marker naming this source or with the source already gone, resumes
  without a second copy rather than refusing. It removes the leftover source
  after reference rewrites; see
  [`internal/rewrite/README.md`](../../rewrite/README.md) §Directory
  promotion for the marker mechanism.
- If removing the physical source directory fails, Apply completes with an
  `ErrResidualSourceDir`-prefixed warning; if removing the old encoded project
  data directory fails, it completes with an `old encoded project data
  directory still present` warning.

#### Refused

- Moves where old and new paths encode to the same directory, or where the
  new encoded directory already exists without a valid promotion marker while
  its old source is still present: refused before any write via
  `checkEncodedDirCollision`.

#### Not covered

- `tasks/.lock` and `tasks/.highwatermark` are not copied and not rewritten.
  They are runtime-only artifacts.

### Malformed history entries preserved

`~/.claude/history.jsonl` is expected to hold one JSON object per line. If a
line fails to parse, cc-port cannot reconstruct the intended data from what
was written. Repairing broken lines is out of scope.

#### Handled

- `MoveSurfaces` reports malformed history line numbers as move warnings.
  `Apply` rewrites well-formed lines through `StreamHistoryJSONL` while a
  malformed line passes through unchanged. The sessions surface reports each
  unparseable `sessions/*.json` file by name and retains its original bytes.

#### Refused

None at runtime. Malformed lines never block the rewrite.

#### Not covered

- Automatic repair. cc-port does not attempt to reconstruct, drop, or
  quarantine a broken line. The original bytes land back on disk unchanged.
- Detection outside `history.jsonl`. Session transcripts and session subdir
  files are rewritten as opaque byte streams with path-boundary-aware
  substitution, not scanned for parse errors.

### File-history handling (move)

Snapshots under `~/.claude/file-history/<session-uuid>/` are opaque byte
streams (see `docs/architecture.md` §File-history policy (cross-cutting)).
This section covers the move-specific handling.

#### Handled

- `MoveSurfaces` performs no file-history surface at all: snapshots are keyed
  by session UUID, not by project path, so a move never needs to relocate
  them. `ResidualWarnings` reports the count of snapshots left in place via
  `snapshotPaths`, warning that their bodies may still contain the old
  project path.

#### Refused

None at runtime. The move never refuses based on snapshot content.

#### Not covered

- Stale path strings inside snapshots after a move. Grepping
  `~/.claude/file-history/` for the old project path still returns hits
  after a successful move, by design. Rewind continues to work because it
  resolves by filename, not by content.

### Anonymisation (export)

Called by `cc-port export` for every full export.

#### Handled

- Every body written to the archive passes through `sink.ApplyPlaceholders`
  before hitting the ZIP. This applies to sessions, memory, history, config,
  and every session-keyed group.
- `{{PROJECT_DIR}}` is declared unconditionally in `Placeholders` from
  `Home.ProjectDir(project)`, not discovered, because the encoded storage
  reference lives in session-subdir bodies that `gatherDiscoveryContent`
  does not scan. `applyPlaceholders` substitutes it before `{{HOME}}` via
  longest-first ordering.
- `discoverPlaceholders` anchors candidate paths under the project path and
  the current machine's home directory (`homeAnchor`), emitting at most two
  suggestions (`{{PROJECT_PATH}}`, `{{HOME}}`); an unanchored path is
  dropped, never guessed at.
- File-history snapshots are the one exception: see §File-history handling (export).

#### Refused

A partial-scrub pass on file-history bytes is refused. The category flag is
the only opt-out surface.

#### Not covered

- Privacy of snapshot content inside an exported archive. If the archive is
  shared, the recipient sees literal project paths embedded in any snapshot
  that quoted them.

### Session-keyed zip layout (export)

Driven by `exportSessionKeyed`, which iterates `locations.AllFlatFiles()`
(registry order) and skips any group whose category is not selected. Each
entry's zip prefix and relative-path base come from the matching
`Registries` row (`ZipPrefix`, `HomeBaseDir`); there are no per-group
helpers.

#### Handled

The session-keyed groups (`todos`, `usage-data/session-meta`,
`usage-data/facets`, `plugins-data`, `tasks`) are written by one loop driven
entirely by `Registries`.

#### Refused

Hard-coding a zip prefix or home base directory in this file is refused. All
layout comes from `Registries`.

#### Not covered

Adding a new session-keyed group. That requires appending to `Registries`,
not editing `export.go`.

### File-history handling (export)

#### Handled

When `file-history` is selected, each snapshot is written verbatim under
`file-history/<uuid>/...` via `addDirVerbatimToZip`. No path anonymisation
runs. `Export` returns a `tool.ExportResult` whose `Categories["file-history"]`
carries one entry per snapshot, and a warning naming the count when the
slice is non-empty.

#### Refused

Inspecting or rewriting snapshot bytes is refused. Snapshots are opaque
user-file bytes.

#### Not covered

Privacy of exported snapshots. Excluding the category (omitting
`--include claude/file-history`, or omitting all category flags on a
non-`--all` invocation) is the entire opt-out surface.

### History-line project attribution (export)

`historyLineBelongsToProject` classifies one `history.jsonl` line by three
rules, applied in order: (1) if the line parses and its `project` field
equals `project`, it belongs; (2) if the line parses and its `project` field
is a non-empty different value, it does not belong; (3) if the line does not
parse, or parses with an empty `project` field, membership falls back to a
boundary-aware substring scan (`rewrite.ContainsBoundedPath`) against the
raw line bytes.

#### Handled

Every well-formed line with a matching `project` field is included without a
substring scan; every malformed or `project`-less line still gets a
substring-based inclusion test rather than being silently dropped.

#### Refused

None at runtime.

#### Not covered

A line whose `project` field names a different project but which also
happens to reference this project's path in free text (for example, a
pasted error mentioning both projects) is excluded, matching rule (2)'s
precedence over the substring scan.

### Source mtime preservation (Claude adapter)

Used by `cc-port import` and `cc-port pull` to restore the chronological
ordering of imported files (Claude Code's `/resume` picker orders sessions
by mtime).

#### Handled

- Export: every verbatim archive entry carries the source file's mtime in
  `FileHeader.Modified`. `writeJSONLFile` and the file-history writer stat
  their open source and pass `ModTime()` through.
- Move: transcripts and memory files restore their pre-rewrite mtime via
  `rewriteTwicePreservingMtime`; session-keyed flat files restore theirs via
  `os.Chtimes` after `rewriteTracked`.
- Import: `archive.StageSibling` receives `entry.Modified` and applies it to
  the staged temp before promotion.

#### Refused

None at runtime. A `Stat` or `Chtimes` failure aborts the operation.

#### Not covered

`metadata.xml`, `history.jsonl`, and `config.json` carry no per-file
timestamp; each is synthesized or merged, so the entry's `Modified` is left
zero and the destination inherits its natural write-time mtime.

### Stage and Finalize (import)

`Stage` routes one archive entry (already stripped of the `claude/` prefix)
to a sibling temp via `archive.StageSibling`, or, for `history/history.jsonl`
and `config.json`, buffers it for `Finalize` because both need a
read-merge-write against existing content rather than plain promotion.

#### Handled

- `finalizeHistory` appends every new line to `history.jsonl`, deduplicating
  by exact line against both existing content and lines already appended in
  the same run, so a re-run of the same import never duplicates a line.
- `finalizeConfig` splices the buffered project block into `~/.claude.json`
  under the target path's key via `sjson.SetRawBytes`, preserving every byte
  outside the inserted entry; re-running with the same block is naturally
  idempotent.
- `ImplicitAnchors` supplies `{{PROJECT_PATH}}` (the import target),
  `{{HOME}}` (the import machine's real home directory, independent of any
  `--claude-home` override), and `{{PROJECT_DIR}}` (the target's encoded
  storage directory).

#### Refused

- An archive entry whose Claude-relative name matches no known prefix:
  `UnknownArchiveEntryError`.
- `mergeProjectConfigBytes` against an existing `.claude.json` that is not
  valid JSON: `InvalidConfigJSONError`.

#### Not covered

- Cross-run history growth bounds. Every import appends only genuinely new
  lines, but a long history of repeated distinct imports still grows the
  file; there is no compaction.

### File-history handling (import)

#### Handled

`Stage` routes `file-history/` entries to a sibling temp with
`resolutions == nil`, so no placeholder substitution runs over snapshot
bodies; they are written back byte-for-byte.

#### Refused

None at runtime.

#### Not covered

None at runtime. The opaque-bytes policy means content interpretation is out
of scope.

### Reference and disk accounting (stats)

Reference counts route through the `rewrite` count primitives, never
`strings.Count`, so the boundary contract holds in one place (see
[`internal/rewrite/README.md`](../../rewrite/README.md) §Boundary rules).
Each surface uses the count variant whose escaping matches how `Apply`
encodes a path on that surface:

| Surface | Variant | Reason |
|---|---|---|
| `history`, `sessions`, `config` | JSON-escape | `Apply` rewrites these through the typed JSON helpers, which can emit `\/`. |
| `transcripts`, `memory` | raw, plus a raw encoded-dir pass | `Apply` rewrites these with the plain replacer, and each embeds the encoded `~/.claude/projects/<encoded>` form. |
| user-wide and session-keyed flat files | raw | `Apply` rewrites these with the plain replacer. |

#### Handled

- A path bounded by a sibling prefix (`/p/myproject-extras` while counting
  `/p/myproject`) does not count, because the boundary rule rejects it.
- `DiskCategories` and `EnumerateProjects` key disk usage by category name
  and order it by `categories` (this package's export-category table), so
  `history` and `config` (shared globals with no per-project disk footprint)
  are present in the result at zero rather than omitted.

#### Refused

A reference count for `file-history`. Snapshot bytes are opaque and never
scanned.

#### Not covered

A move applied without `--deep` leaves transcripts untouched, so the
unconditional transcript reference count reflects what such a move *would*
touch if the flag were set, not a default move.

## Tests

Unit tests in `paths_test.go`, `locations_test.go`, `schema_test.go`. Coverage:

- encoding round-trips for representative paths.
- symlink resolution with and without trailing non-existent components.
- round-trip marshal and unmarshal of each schema type.
- `LocateProject` hit and miss paths, including the identity guard's match,
  contradiction, and no-witness outcomes.
- `EnumerateProjects` (in `enumerate_test.go`): witness-resolved labels
  including the lossy-encoding case, the multi-witness disagreement tie-break,
  the witness-less label fallback, non-directory skip, and the empty projects
  directory.

Fuzz target `FuzzVerifyProjectIdentity` in `locations_fuzz_test.go` asserts
the identity guard's three-state outcome is deterministic under arbitrary
projectPath and cwd byte sequences. Reached via the test-only
`VerifyProjectIdentityForTest` shim in `export_test.go`.

Move, export, import, and stats coverage: `move_internal_test.go`
(`rewriteTracked` happy path and failure modes),
`export_filehistory_test.go` (unreadable-snapshot and unreadable-dir
failure, zip-write failure, context cancellation mid-walk),
`export_line_cap_test.go` and `export_mtime_internal_test.go` (the
`MaxHistoryLine` cap and source-mtime preservation through `writeJSONLFile`),
`discover_internal_test.go` (`discoverPaths`/`autoDetectPlaceholders`/`discoverPlaceholders`,
including a corpus of prose fragments discovery must not mistake for a real
path: base64 blobs, GitHub URL and ref fragments, RuboCop cop names, tilde
paths, pseudo-XML tags, bare filenames in prose, and an assertion that the
planted project anchor placeholder survives discovery), `import_merge_internal_test.go`
(`finalizeHistory`'s newline-boundary handling and `mergeProjectConfigBytes`'s
sibling-key preservation and invalid-JSON refusal),
`session_keyed_groups_drift_test.go` (every `Registries` session-keyed
entry's `Category` matches a name this adapter's `Categories()` declares),
and `witness_test.go` (`FindActive` on a live vs. a dead session PID).

The root `integration_test.go`'s `TestIntegration_ExportImportRoundTrip_AllCategories`
drives a full export-import round trip across every category and, via
`assertFileHistorySnapshotsByteIdentical`, byte-compares every file-history
snapshot end to end, so a snapshot altered or dropped in transit fails the
test.
