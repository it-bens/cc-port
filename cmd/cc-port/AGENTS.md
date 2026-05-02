# cmd/cc-port — agent notes

## Before editing

- Build every command via `newXCmd(claudeDir *string) *cobra.Command`. No package-level command vars, no package-level flag-value vars, no `init()` blocks for flag wiring (README §Constructor pattern).
- Route every from-manifest exclusivity check through `applyCategorySelection`. Never re-implement the rule per cmd (README §Category selection).
- Render rules-file warnings via `renderRulesReport` consuming `Result.RulesReport` / `Plan.RulesReport`. `runExportManifest` is the single legitimate inline-scan site (README §Rules-warning routing).
- Write every cmd output through `cmd.OutOrStdout()` and `cmd.ErrOrStderr()`. No bare `fmt.Printf` / `fmt.Println` / `os.Stderr` writes (README §Stream routing).
- Refuse user-supplied resolutions for implicit keys via `importer.IsImplicitKey(key)`. Never compare against a literal token (`internal/importer/README.md` §Placeholder handling).

## Documented carve-outs

- `move.go:findActive` is a package-level test seam for `lock.FindActive`, swapped via `withMoveSeams` with `t.Cleanup`. It is an injection point for an external dependency, not the package-var class of bug the constructor pattern targets.

## Navigation

- Entry: `main.go:newRootCmd`, `main.go:main`.
- Commands: `move.go:newMoveCmd`, `export.go:newExportCmd`, `importcmd.go:newImportCmd`, `pushcmd.go:newPushCmd`, `pullcmd.go:newPullCmd`.
- Cmd-side helpers: `category_selection.go:applyCategorySelection`, `render.go:renderRulesReport`, `categories.go:registerCategoryFlags`.
- Test seams: `move.go:findActive` (see Documented carve-outs).
- Tests: `*_test.go` in this directory.
