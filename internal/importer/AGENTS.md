# internal/importer: agent notes

## Before editing

- Refuse any archive with an unresolved declared key that is actually referenced in a body. Undeclared `{{UPPER_SNAKE}}` tokens in bodies are content; preserve them verbatim (README §Import contract).
- This package is generic orchestration; it must never import a tool adapter. Per-tool staging and merge logic lives in each adapter's `Stage`/`Finalize` (e.g. `internal/tool/claude`). (README §Import contract)
- Drive all-or-nothing promotion through `rewrite.SafeRenamePromoter` over every tool's staged files as one batch. Do not bypass it on partial failure (README §Import contract and `internal/rewrite/README.md` §Boundary rules).
- Route manifest category validation through `manifest.ApplyToolCategories`. Hard-fail on an archive entry whose leading path segment names an unregistered tool (README §Import contract and `internal/manifest/README.md` §Category manifest).
- A registered tool absent from the manifest is reported and skipped; do not treat it as a hard failure (README §Import contract).
- Keep `DryRun` write-free and lock-free, and route it through the same preflight gates `runLocked` runs (README §Plan surface).
- Never reach `Stage` or `Finalize` from `DryRun`; both write (README §Plan surface).

## Navigation

- Entry: `importer.go:Run`; plan entry: `plan.go:DryRun`, rendered by `render.go`.
- Promotion: `promote.go:promoteStaged`.
- Cap enforcement, containment, and placeholder streaming: `internal/archive`.
- Tests: `importer_test.go`, `importer_large_test.go` (`-tags large`).
