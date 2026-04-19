# internal/importer — agent notes

Apply a cc-port archive: validate, stage, promote, roll back. See `README.md` for the full contracts.

## Before editing

- Every archive is a closed contract — reject on any unresolved declared key or any undeclared `{{UPPER_SNAKE}}` token in bodies; do not add a "best-effort" fallback (README §Import contract).
- Placeholder classification reads the manifest, not body grammar — do not parse tokens directly out of ZIP entry content (README §Placeholder resolution).
- Every destination stages at a `*.cc-port-import.tmp` sibling resolved through `EvalSymlinks`; `os.Rename` must stay intra-filesystem, so never form the temp path against the raw parent when the parent is a symlink (README §Atomic staging).
- `SafeRenamePromoter.Rollback` drives all-or-nothing promotion — do not bypass it on partial failure; every earlier rename must be reversed (README §Atomic staging and `internal/rewrite/README.md`).
- File-history snapshots are opaque bytes; `ResolvePlaceholders` runs over them only for pre-refactor archive compatibility (README §File-history handling (import) and docs/architecture.md §File-history policy (cross-cutting)).
- `importer.Run` wraps its body in `lock.WithLock` before reading the archive (see `internal/lock/README.md` §Concurrency guard).
- Manifest category validation routes through `manifest.ApplyCategoryEntries`; never re-implement the check here (see `internal/manifest/README.md` §Category manifest).
- Unknown ZIP entry prefixes hard-fail; there is no tolerant fallback (README §Strict archive contract).
- Session-keyed dispatch reads `transport.SessionKeyedTargets` — the first matching `ZipPrefix` wins, and every staged entry lands in the unified `importPlan.sessionKeyedStagedFiles` slice. Do not add a per-group staging helper or a parallel slice (README §Session-keyed prefix arms).
- Every archive-entry write routes through `os.Root` — `stageIntoRoot` for project/memory writes, `assertWithinRoot` for the file-history and session-keyed sibling-temp writers. Never call `filepath.Join(base, zipName)` directly when staging (README §Atomic staging).

## Navigation

- Entry: `importer.go:Run`.
- Classification: `resolve.go:ClassifyPlaceholders`, `resolve.go:ValidateResolutions`.
- Staging preflight: `importer.go:stagingTempPath`, `importer.go:checkStagingFilesystems`.
- Conflict check: `resolve.go:CheckConflict`.
- Tests: `importer_test.go`, `resolve_test.go`.

Read `README.md` before changing anything under `## Contracts`.
