# internal/export — agent notes

Produce a cc-port archive + manifest for one project. See `README.md` for the full contract.

## Before editing

- File-history snapshots are archived byte-for-byte; no path anonymisation runs over those bytes — do not add a scrub pass (README §File-history handling (export) and root README §File-history policy).
- The `--file-history=false` opt-out (and its implicit form — omitting the category when other categories are explicitly selected) is the only privacy surface; do not introduce a partial-scrub alternative (README §File-history handling (export) §Not covered).
- Every key the export embeds in bodies must also be declared in `metadata.xml` as a `<placeholder>` entry — the manifest is the source of truth the importer reads (see `internal/importer/README.md` §Import contract).
- Path anonymisation must be order-independent across runs: a re-export of the same project must produce the same placeholder set (README §File-history handling (export) — covered by `export_test.go:TestExport_PathAnonymization_OrderIndependent`).

## Navigation

- Entry: `export.go:Run`.
- Discovery: `discover.go:DiscoverPaths`, `discover.go:GroupPathPrefixes`, `discover.go:AutoDetectPlaceholders`.
- Manifest I/O: `manifest.go:WriteManifest`, `manifest.go:ReadManifest`, `manifest.go:ReadManifestFromZip`.
- Tests: `export_test.go`, `discover_test.go`, `manifest_test.go`.

Read `README.md` before changing anything under `## Contracts`.
