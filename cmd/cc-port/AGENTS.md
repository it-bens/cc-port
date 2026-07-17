# cmd/cc-port agent notes

## Pointer map

- Composition root and persistent flags: `main.go:newRootCmd`, `tools.go:newToolSet`, `toolselect.go:registerToolFlags` (README §Purpose)
- Target selection and tool-home overrides: `toolselect.go:resolveTargets` (README §Purpose)
- Category flags and validation: `categories.go:registerCategoryFlags`, `categories.go:resolveSelectionFromCmd` (README §Category selection)
- Manifest category selection and placeholder discovery: `category_selection.go:applyCategorySelection`, `category_selection.go:resolveCategoriesAndPlaceholders` (README §Category selection)
- Command factories: `move.go:newMoveCmd`, `export.go:newExportCmd`, `importcmd.go:newImportCmd`, `pushcmd.go:newPushCmd`, `pullcmd.go:newPullCmd`, `stats.go:newStatsCmd` (README §Commands)
- Banner interface and build-tag binding: `banner.go:Banner`, `banner_default.go`, `banner_logo.go` (README §Banner DI)
- Command output uses Cobra command writers: `cmd.OutOrStdout`, `cmd.ErrOrStderr` (README §Stream routing)
- Command tests are `*_test.go` in this directory (README §Tests)
