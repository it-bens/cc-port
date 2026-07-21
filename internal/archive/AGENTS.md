# internal/archive: agent notes

## Before editing

- Route every new staged write through `assertWithinRoot` (or `StageSibling`, which already does); never write a caller-supplied relative path directly. (README §os.Root containment)
- Check both the declared-size cap and the post-decode byte count on every new entry-reading path; a single check lets a misdeclared size slip through. (README §Entry decompression caps)
- Feed every entry read into the caller's `AggregateCounter`, including bytes read only for classification. (README §Entry decompression caps)
- Split a new archive entry name on its tool prefix through the existing helper; never hand-parse a `<tool>/` segment inline. (README §Per-tool prefixes)

## Navigation

- Caps: `caps.go`.
- Reading and per-entry cap enforcement: `reader.go`.
- Placeholder classification and resolution: `classify.go`, `resolve.go`.
- Writing (Sink): `sink.go`.
- Staging (os.Root containment): `stage.go`.
- Tests: `archive_test.go`, `resolve_test.go`, `mtime_test.go`.
