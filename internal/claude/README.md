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
    `UsageDataDir`, `PluginsDataDir`, `TasksDir`.
- **Project enumeration**
  - `LocateProject(claudeHome *Home, projectPath string) (*ProjectLocations, error)`:
    returns every file tied to a project. Errors if the project directory
    does not exist. Optional resources are zero-valued when absent.
  - `ProjectLocations`: struct holding the set.
- **Session-keyed registry**
  - `SessionKeyedGroup`: descriptor struct with `Name` (stable machine key
    and display label), `Files func(*ProjectLocations) []string`, and
    `SidecarFilter func(name string) bool`.
  - `SessionKeyedGroups`: ordered slice that is the registry. Slice order
    is the display and iteration order used by every downstream consumer.
  - `(*ProjectLocations).AllFlatFiles() iter.Seq2[SessionKeyedGroup, string]`:
    yields `(group, absolute path)` pairs in registry order, applying
    each group's `SidecarFilter`. Performs no I/O and supports early
    termination via `break`.
- **Schemas**
  - `HistoryEntry`, `SessionFile`, `UserConfig`, `SettingsMarketplace`,
    `SettingsMarketplaceSource`: Go types for the JSON/JSONL files
    cc-port reads and writes. `HistoryEntry`, `SessionFile`, and
    `UserConfig` implement `json.Marshaler` and `json.Unmarshaler`,
    preserving unknown fields in an `Extra` map.

## Contracts

### Path encoding

cc-port identifies every project by its encoded directory name under
`~/.claude/projects/`. The encoding is inherited from Claude Code. The
input path is resolved through the filesystem following symlinks, then `/`,
`.`, and space each map to `-`, and a leading `-` is prepended.

The encoding is lossy. Three distinct paths collapse to the same name:

- `/Users/x/Projects/my project`
- `/Users/x/Projects/my-project`
- `/Users/x/Projects/my.project`

All three encode to `-Users-x-Projects-my-project`. cc-port applies the same
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

- **Pre-existing collisions.** If two distinct paths were already stored in
  the same encoded directory before cc-port ran, the data is interleaved.
  Operations targeting either path will read and write the shared storage.
- **Decoding a directory name back to a path.** One encoded name maps to
  any of several real paths. cc-port never decodes. Every operation takes
  the original path as input and encodes forward. To find the owner of a
  stored directory, read `cwd` from a `sessions/*.json` file or the
  matching `~/.claude.json` project key.

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

#### Refused

- None at runtime. `LocateProject` fails hard when the project directory
  does not exist. Optional directories silently return empty.

#### Not covered

- None inherent to project enumeration. Callers that need an exhaustive
  walk use `AllFlatFiles()`, which applies the sidecar filter automatically.

### Session-keyed registry

The five session-keyed groups are published as a canonical registry.
Downstream consumers (move, export, import, CLI renderers) iterate the
registry rather than open-coding group names. Adding a sixth group requires
one entry in `SessionKeyedGroups` and one index-aligned entry in
`transport.SessionKeyedTargets`. The alignment test in `internal/transport`
catches drift between those two slices.

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

- None inherent to the registry. Extension (adding a sixth group) is a
  compile-time edit, not a runtime concern.

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

## Tests

Unit tests in `paths_test.go`, `locations_test.go`, `schema_test.go`. Coverage:

- encoding round-trips for representative paths.
- symlink resolution with and without trailing non-existent components.
- round-trip marshal and unmarshal of each schema type.
- `LocateProject` hit and miss paths.
