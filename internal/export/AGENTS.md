# internal/export -- agent notes

## Before editing

- Archive file-history snapshots verbatim; never add a scrub pass over those bytes. (README §File-history handling (export))
- Never introduce a partial-scrub alternative; `--file-history=false` is the only privacy surface for snapshots. (README §File-history handling (export))
- Route all category declarations through `manifest.BuildCategoryEntries`; never hand-roll a parallel nine-entry literal. (README §Category coverage)
- Declare every placeholder key written into bodies as a `<placeholder>` entry in `metadata.xml`. (internal/importer/README.md §Import contract)
- Resolve all zip prefixes and home base directories from `transport.SessionKeyedTargets`; never hard-code them here. (README §Session-keyed zip layout)
- Preserve anonymisation order-independence: re-export of the same project must produce the same placeholder set. (README §Anonymisation)

## Navigation

- Entry: `export.go:Run`.
- Discovery: `discover.go:DiscoverPaths`, `discover.go:GroupPathPrefixes`, `discover.go:AutoDetectPlaceholders`.
- Wire DTOs + manifest I/O: `internal/manifest`.
- Tests: `export_test.go`, `discover_test.go`.
