## Before editing

- Do not normalise unicode or casefold in `EncodePath` (README §Path encoding).
- Never decode an encoded directory name back to a real path; read `cwd`
  from a session JSON or a `~/.claude.json` project key instead (README
  §Path encoding).
- Do not call `filepath.EvalSymlinks` directly on user paths; use
  `ResolveProjectPath` (README §Path encoding;
  `internal/fsutil/README.md §Absolute-path contract for ResolveExistingAncestor`).
- Return empty (not an error) from new session-keyed location collectors
  when the parent directory is absent (README §Project enumeration).
- When adding a sixth session-keyed group, append one entry to
  `SessionKeyedGroups` and one index-aligned entry to
  `transport.SessionKeyedTargets` (README §Session-keyed registry).
- Any new `bufio.Scanner` reader of `history.jsonl` must cap the buffer
  with `MaxHistoryLine` (README §History line cap).

## Navigation

- Encoding: `paths.go:EncodePath`, `paths.go:ResolveProjectPath`.
- Home and derived paths: `paths.go:NewHome`, `paths.go:Home`.
- Project enumeration: `locations.go:LocateProject`.
- Session-keyed registry: `session_keyed_groups.go`.
- Schemas and constants: `schema.go` (`HistoryEntry`, `MaxHistoryLine`).
- Tests: `paths_test.go`, `locations_test.go`, `schema_test.go`.
