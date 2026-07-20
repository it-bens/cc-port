# internal/tool/claude: agent notes

## Before editing

- Do not normalise unicode or casefold in `EncodePath` (README §Path encoding).
- Do not decode an encoded directory name (README §Path encoding).
- Return empty from new session-keyed collectors when the parent is absent (README §Project enumeration).
- Add each session-keyed directory as one `Registries` row (README §Session-keyed registry).
- Add each user-wide rewrite target as one `Registries` row and one `Home` method (README §User-wide registry).
- Cap each new `history.jsonl` scanner with `MaxHistoryLine` (README §History line cap).
- Never scrub or rewrite file-history snapshot bytes on any surface (export, import, move). (README §File-history handling (move), §File-history handling (export), §File-history handling (import))

## Navigation

- Encoding: `home.go` (`EncodePath`).
- Home and derived paths: `home.go` (`NewHome`, `Home`).
- Project enumeration: `locations.go:LocateProject`.
- Transcript body file set (shared by move and stats): `transcripts.go:TranscriptFiles`.
- Registries: `registries.go`.
- Schemas and constants: `schema.go` (`HistoryEntry`, `MaxHistoryLine`).
- Tool contract: `adapter.go`, `categories.go`.
- Export: `export.go`, `discover.go`.
- Import: `import.go`.
- Move: `move.go`.
- Stats: `stats.go`.
- Tests: `paths_test.go`, `locations_test.go`, `schema_test.go`.
