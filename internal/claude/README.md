# internal/claude

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
  - `ResolveProjectPath(path string) (string, error)`: resolves
    user-supplied paths through symlinks, preserving any non-existent
    tail. Delegates to `fsutil.ResolveExistingAncestor` (see
    [`internal/fsutil/README.md`](../fsutil/README.md)) after calling
    `filepath.Abs`.
- **Home**
  - `NewHome(override string) (*Home, error)`: constructs a `~/.claude`
    root, honouring `--claude-dir`.
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
  - `SessionKeyedGroup`: descriptor struct with `Name` (stable machine key
    and display label), `Category` (the controlling `manifest.AllCategories`
    entry name for export filtering),
    `Files func(*ProjectLocations) []string`, and
    `SidecarFilter func(name string) bool`.
  - `SessionKeyedGroups`: ordered slice that is the registry. Slice order
    is the display and iteration order used by every downstream consumer.
  - `(*ProjectLocations).AllFlatFiles() iter.Seq2[SessionKeyedGroup, string]`:
    yields `(group, absolute path)` pairs in registry order, applying
    each group's `SidecarFilter`. Performs no I/O and supports early
    termination via `break`.
- **User-wide registry**
  - `UserWideRewriteTarget`: descriptor struct with `Name` (stable machine
    key and display label) and `Path func(*Home) string`.
  - `UserWideRewriteTargets`: ordered slice that is the registry. Slice
    order is the display and iteration order used by every downstream
    consumer. Consumed by `internal/move`; `internal/export` and
    `internal/importer` intentionally do not iterate it (these files are
    machine-local and do not belong in a cross-machine archive).
- **Schemas**
  - `HistoryEntry`, `SessionFile`, `UserConfig`, `SettingsMarketplace`,
    `SettingsMarketplaceSource`: Go types for the JSON/JSONL files
    cc-port reads and writes. `HistoryEntry`, `SessionFile`, and
    `UserConfig` implement `json.Marshaler` and `json.Unmarshaler`,
    preserving unknown fields in an `Extra` map.
  - `MaxHistoryLine`: 16 MiB ceiling for a single `history.jsonl` line
    read through `bufio.Scanner`. Shared by every scanner in the
    codebase that reads `history.jsonl`.

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

The consumers that enforce the "refused on collision" behaviour:

- `internal/move/README.md §Malformed history entries preserved`: aborts
  when old and new encode identically, or when the target encoded directory
  already exists.
- `internal/importer/README.md §Import contract`: `CheckConflict` refuses
  when the encoded target directory already exists.

#### Handled

- Encoding input paths that contain `/`, `.`, or space: each is mapped to
  `-`. Paths that begin with `/` gain a leading `-`.
- Symlink resolution via `ResolveProjectPath` before encoding, so the
  result matches what Claude Code wrote.

#### Refused

- `cc-port move` (apply or dry-run) where old and new paths encode to the
  same directory name. The copy-and-delete sequence cannot run against a
  single on-disk location. Proceeding would destroy data.
- `cc-port move` (apply or dry-run) where the target encoded directory
  already exists. Another project path has claimed that storage. Proceeding
  would silently merge or overwrite its data.
- `cc-port import` where the target encoded directory already exists. Same
  reasoning.

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

The session-keyed groups are published as a canonical registry.
Downstream consumers (move, export, import, CLI renderers) iterate the
registry rather than open-coding group names. Adding a new group requires
one entry in `SessionKeyedGroups` and one index-aligned entry in
`transport.SessionKeyedTargets`. The alignment test in `internal/transport`
catches drift between those two slices.

Each group's `Category` field names the `manifest.AllCategories` entry
that gates its export. The two `usage-data/*` groups both carry
`"usage-data"`, so a single category flag covers both subgroups.
A drift-guard test in `internal/claude/session_keyed_groups_drift_test.go`
fails when a group ships with a Category outside `manifest.AllCategories`.

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

`UserWideRewriteTargets` lists the user-wide files whose bytes may contain
references to a project path and can be rewritten by component-boundary-aware
byte replacement. Current entries: `settings` (`~/.claude/settings.json`),
`plugins/installed_plugins` (`~/.claude/plugins/installed_plugins.json`),
`plugins/known_marketplaces` (`~/.claude/plugins/known_marketplaces.json`).

Files with structurally distinct rewriters stay outside the registry:
`history.jsonl` (JSONL streaming), session files under
`~/.claude/sessions/*.json` (JSON round-trip via `rewrite.SessionFile`), and
`~/.claude.json` (JSON round-trip via `rewrite.UserConfig`). Forcing them in
would require a strategy field on every entry.

Adding a user-wide file means one entry in `UserWideRewriteTargets` and one
`Home` path-derivation method on `paths.go`.

#### Handled

- Registry iteration in `internal/move` walks `UserWideRewriteTargets` once
  in `rewriteUserWideFiles` (Apply) and once in `countUserWideReplacements`
  (DryRun). Both use the same slice order.
- Missing target files contribute zero to DryRun counts and are skipped at
  Apply (matching the existing settings-absent behavior).

#### Refused

- None at runtime. The registry is a package-level var. Callers read it and
  do not add to it at runtime.

#### Not covered

- `internal/export` and `internal/importer` do not consume the registry.
  Plugin-registry files are machine-local and stay out of archives. A future
  iteration can add archive handling by introducing an index-aligned
  descriptor slice, matching the
  `SessionKeyedGroups` ↔ `transport.SessionKeyedTargets` pattern.

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
  `MaxHistoryLine`: `rewrite.StreamHistoryJSONL` (move's rewrite path) and
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
