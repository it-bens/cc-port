# internal/claude — agent notes

Claude Code data layout: path encoding, project locations, schemas. See `README.md` for the full contract.

## Before editing

- `EncodePath` mirrors Claude Code's lossy encoding exactly — do not normalise unicode or casefold; the encoded name must byte-for-byte match what Claude Code writes (README §Path encoding).
- Never try to decode an encoded directory name back to a real path; the mapping is many-to-one. Read `cwd` from a session JSON or a `~/.claude.json` project key instead (README §Path encoding §Not covered).
- `ResolveProjectPath` preserves any non-existent trailing components by delegating to `fsutil.ResolveExistingAncestor`; don't call `filepath.EvalSymlinks` directly on user paths (README §Path encoding; see `internal/fsutil/README.md` §Absolute-path contract for `ResolveExistingAncestor`).
- New session-keyed location collectors (`collectTodos`, `collectUsageData`,
  `collectPluginsData`, `collectTaskFiles`) return empty when their parent
  directory is absent — matches `collectMemoryFiles` (README §Project
  enumeration).

## Navigation

- Encoding: `paths.go:EncodePath`, `paths.go:ResolveProjectPath`.
- Home + derived paths: `paths.go:NewHome`, `paths.go:Home`.
- Project enumeration: `locations.go:LocateProject`.
- Schemas: `schema.go`.
- Tests: `paths_test.go`, `locations_test.go`, `schema_test.go`.

Read `README.md` before changing anything under `## Contracts`.
