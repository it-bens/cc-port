# internal/export -- agent notes

## Before editing

- This package is generic orchestration; it must never import a tool adapter. Per-tool export logic (zip prefixes, home base directories, category bodies) lives in each adapter (e.g. `internal/tool/claude`). (README §Category coverage)
- Route every tool's category declarations through `manifest.BuildToolCategoryEntries`; never hand-roll a parallel category literal. (README §Category coverage)
- A target reporting `tool.ErrProjectAbsent` writes an empty tool block; it must not fail the whole run. (README §Category coverage)

## Navigation

- Entry: `export.go:Run`.
- Wire DTOs + manifest I/O: `internal/manifest`.
- Archive writing (per-tool prefix, caps, placeholder substitution): `internal/archive`.
- Tests: `export_test.go`.
