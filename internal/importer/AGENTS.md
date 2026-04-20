# internal/importer — agent notes

Apply a cc-port archive: validate, stage, promote, roll back. See `README.md` for the full contracts.

## Before editing

- Refuse any archive with an unresolved declared key or an undeclared `{{UPPER_SNAKE}}` token. No best-effort fallback (README §Import contract).
- Read the manifest first when classifying placeholders. Never fall back to parsing tokens from ZIP entry content (README §Placeholder handling).
- Form every staging temp via `fsutil.ResolveExistingAncestor`. Never use the raw parent path when the parent may be a symlink (README §Atomic staging).
- Drive all-or-nothing promotion through `SafeRenamePromoter`. Do not bypass it on partial failure (README §Atomic staging and `internal/rewrite/README.md` §Boundary rules).
- Never inspect or rewrite file-history snapshot contents (README §File-history handling (import) and `docs/architecture.md` §File-history policy (cross-cutting)).
- Route manifest category validation through `manifest.ApplyCategoryEntries`. Hard-fail on unknown ZIP entry prefixes (README §Strict archive contract and `internal/manifest/README.md` §Category manifest).
- Use `transport.SessionKeyedTargets` for every session-keyed dispatch. Do not add per-group staging helpers or a parallel slice (README §Atomic staging).

## Navigation

- Entry: `importer.go:Run`.
- Classification: `resolve.go:ClassifyPlaceholders`, `resolve.go:ValidateResolutions`.
- Staging preflight: `importer.go:stagingTempPath`, `importer.go:checkStagingFilesystems`.
- Conflict check: `resolve.go:CheckConflict`.
- Tests: `importer_test.go`, `resolve_test.go`, `resolve_fuzz_test.go`.
