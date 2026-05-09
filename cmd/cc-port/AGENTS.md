# cmd/cc-port — agent notes

## Before editing

- Build every command via `newXCmd(claudeDir *string) *cobra.Command`. No package-level command vars, no package-level flag-value vars, no `init()` blocks for flag wiring (README §Constructor pattern).
- Route every from-manifest exclusivity check through `applyCategorySelection`. Never re-implement the rule per cmd (README §Category selection).
- Render rules-file warnings via `renderRulesReport` consuming `Result.RulesReport` / `Plan.RulesReport`. `runExportManifest` is the single legitimate inline-scan site (README §Rules-warning routing).
- Write every cmd output through `cmd.OutOrStdout()` and `cmd.ErrOrStderr()`. No bare `fmt.Printf` / `fmt.Println` / `os.Stderr` writes (README §Stream routing).
- Refuse user-supplied resolutions for implicit keys via `importer.IsImplicitKey(key)`. Never compare against a literal token (`internal/importer/README.md` §Placeholder handling).
- Build every cobra factory that ultimately reaches `ui.SelectCategories` to take a `Banner` parameter alongside `claudeDir *string` (`newExportCmd`, `newPushCmd`, `newExportManifestCmd`). Factories that don't reach the picker (`newMoveCmd`, `newImportCmd`, `newPullCmd`) skip the banner. `newVersionCmd` is the exception: it takes a `Banner` for help / version rendering but does not need `claudeDir`, so its signature is `newVersionCmd(banner Banner) *cobra.Command`.
- Route the category fall-through and placeholder discovery through `resolveCategoriesAndPlaceholders` (`category_selection.go`). `applyCategorySelection` calls it after the `--from-manifest` branch; `runExportManifest` calls it directly because the manifest subcommand has no `--from-manifest` flag (README §Category selection).
- Never reach for a banner default at the call site. Read `bannerImpl` once in `main()` and thread it through `newRootCmd(banner)` to subcommand factories.

## Documented carve-outs

- `move.go:findActive` is a package-level test seam for `lock.FindActive`, swapped via `withMoveSeams` with `t.Cleanup`. It is an injection point for an external dependency, not the package-var class of bug the constructor pattern targets.
- `bannerImpl` is a build-tag-selected unexported package var (`banner_default.go` declares `noopBanner{}`; `banner_logo.go` declares `logo.Banner{}`). Per `docs/design-rules.md` §"Plug an injectable dependency into a function" this is the allowed unexported-seam shape: the dependency reaches consumers via function parameters; `bannerImpl` is just where the build-tag selection lands so `main()` can read it once.

## Navigation

- Entry: `main.go:newRootCmd`, `main.go:main`, `main.go:newVersionCmd`.
- Banner wiring: `banner.go:Banner`, `banner.go:noopBanner`, `banner_default.go`, `banner_logo.go`.
- Commands: `move.go:newMoveCmd`, `export.go:newExportCmd`, `importcmd.go:newImportCmd`, `pushcmd.go:newPushCmd`, `pullcmd.go:newPullCmd`.
- Cmd-side helpers: `category_selection.go:applyCategorySelection`, `category_selection.go:resolveCategoriesAndPlaceholders`, `render.go:renderRulesReport`, `categories.go:registerCategoryFlags`.
- Test seams: `move.go:findActive` (see Documented carve-outs).
- Tests: `*_test.go` in this directory.
