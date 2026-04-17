# internal/claude

## Purpose

Model of Claude Code's on-disk data layout. Encodes real project paths into the `~/.claude/projects/<encoded>/` directory name, enumerates the files that belong to one project, and declares Go types for the JSON/JSONL schemas cc-port rewrites.

Not a file-rewriting package — this module produces locations and types; `internal/move`, `internal/export`, and `internal/importer` drive the mutations.

## Public API

- **Path encoding**
  - `EncodePath(absPath string) string` — mirror of Claude Code's lossy encoding (`/`, `.`, space → `-`, with leading `-`).
  - `ResolveProjectPath(path string) (string, error)` — resolves user-supplied paths through symlinks, preserving any non-existent tail.
- **Home**
  - `NewHome(override string) (*Home, error)` — constructs a `~/.claude` root, honouring `--claude-dir`.
  - `Home` — struct; `Dir` and `ConfigFile` fields plus path-deriving methods `ProjectsDir`, `ProjectDir`, `HistoryFile`, `SessionsDir`, `SettingsFile`, `RulesDir`, `FileHistoryDir`.
- **Project enumeration**
  - `LocateProject(claudeHome *Home, projectPath string) (*ProjectLocations, error)` — returns every file tied to a project.
  - `ProjectLocations` — struct holding the set.
- **Schemas**
  - `HistoryEntry`, `SessionFile`, `UserConfig`, `SettingsMarketplace`, `SettingsMarketplaceSource` — Go types for the JSON/JSONL files cc-port reads and writes.

## Contracts

### Path encoding

cc-port identifies every project by its encoded directory name under
`~/.claude/projects/`. The encoding is inherited from Claude Code:
the input path is first resolved through the filesystem (following
symlinks), then `/`, `.`, and space are each replaced with `-`, and a `-`
is prepended. It is lossy — three distinct paths collapse to the same
name:

- `/Users/x/Projects/my project`
- `/Users/x/Projects/my-project`
- `/Users/x/Projects/my.project`

All three encode to `-Users-x-Projects-my-project`. cc-port uses the same
encoding (and the same symlink resolution on user-supplied paths) because
the encoded name must match what Claude Code writes on disk; the original
path cannot be recovered from the encoded form.

Refused by cc-port — these operations abort before touching anything:

- `cc-port move` (apply or dry-run) where old and new paths encode to the
  same directory name. The copy-and-delete sequence cannot run against a
  single on-disk location, and proceeding would destroy data.
- `cc-port move` (apply or dry-run) where the target encoded directory
  already exists. Another real project path has claimed that storage;
  proceeding would silently merge or overwrite its data.
- `cc-port import` where the target encoded directory already exists.
  Same reasoning.

Not covered — cases cc-port cannot detect or mitigate:

- **Pre-existing collisions.** If two distinct paths were already stored
  in the same encoded directory before cc-port ran — because Claude Code
  itself wrote both there — the data is interleaved and cc-port cannot
  untangle it. Operations targeting either path will read and write the
  shared storage.
- **Decoding a directory name back to a path.** One encoded name maps to
  any of several real paths. cc-port never tries to decode; every
  operation takes the original path as input and encodes forward. To find
  the owner of a stored directory, read `cwd` from a `sessions/*.json`
  file or the matching `~/.claude.json` project key.

The consumers of this encoding that enforce the "refused on collision" behaviour live outside this package:

- `internal/move/README.md` §Malformed history entries preserved — and the surrounding move plan — aborts when old and new encode identically, or when the target encoded directory already exists.
- `internal/importer/README.md` §Import contract — `CheckConflict` refuses when the encoded target directory already exists.

### Project enumeration

`LocateProject` returns a `ProjectLocations` struct whose fields cover every
file and directory tied to the project. The fields enumerate:

- `HistoryEntries` — `~/.claude/history.jsonl` lines whose `cwd` matches the
  project path.
- `SessionFiles` — `~/.claude/projects/<encoded>/sessions/*.json`.
- `MemoryFiles` — `~/.claude/projects/<encoded>/memory/` subtree.
- `TranscriptFiles` — `~/.claude/projects/<encoded>/*.jsonl` transcripts.
- `SettingsFile` — `~/.claude/settings.json` (global settings; included
  because it may contain project-keyed blocks).
- `TodoFiles` — `~/.claude/todos/<sid1>-agent-<sid2>.json` where **either**
  UUID is in the project's session set. The filename allows for sub-agent
  spawns; both parent and child session UUIDs receive independent visibility.
- `UsageDataSessionMeta` — `~/.claude/usage-data/session-meta/<sid>.json`;
  `<sid>` in session set.
- `UsageDataFacets` — `~/.claude/usage-data/facets/<sid>.json`; `<sid>` in
  session set.
- `PluginsDataDirs` — `~/.claude/plugins/data/<ns>/<sid>/` subtrees; `<sid>`
  in session set. Plugin namespace `<ns>` is opaque and preserved verbatim.
- `TaskDirs` — `~/.claude/tasks/<sid>/`; `<sid>` in session set. `.lock` and
  `.highwatermark` sidecars are runtime-only and excluded at enumerate time.

Each session-keyed collector returns empty when its parent directory is absent
(the directory may not exist if the feature has never been used). This matches
the behaviour of `collectMemoryFiles`.

## Tests

Unit tests in `paths_test.go`, `locations_test.go`, `schema_test.go`. Coverage: encoding round-trip for representative paths, symlink resolution with and without trailing non-existent components, round-trip marshal/unmarshal of each schema type, `LocateProject` hit/miss paths.
