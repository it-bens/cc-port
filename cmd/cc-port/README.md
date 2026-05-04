# cmd/cc-port

Wiring layer for the cc-port CLI.

## Purpose

This package owns flag parsing, stdout formatting, and exit-code mapping. Business logic lives in `internal/*`.

## Commands

- `move`: plans and applies a project path rename, printing a dry-run diff before any write.
- `export`: archives a project and its session-keyed data to a ZIP file, with an optional `manifest` subcommand to write a standalone XML for hand-editing.
- `import`: restores a project from a ZIP archive into a target path, resolving non-implicit placeholders via `--from-manifest`.
- `push`: uploads a project archive to a remote under a stable name, with cross-machine conflict refusal overridable by `--force`.
- `pull`: downloads a named archive from a remote and applies it to a target path, sharing the placeholder-resolution contract with `import`.

See the root `README.md` §Commands for one-line syntax and worked examples. Run `cc-port <subcommand> --help` for the full flag reference.

## Constructor pattern

Every cobra command is a constructor `newXCmd(claudeDir *string) *cobra.Command`. The root constructor `newRootCmd` owns the persistent `--claude-dir` local and threads its address into each subcommand. Flag-value locals live as closure variables inside the constructor body, never as package-level `var`. No `init()` block wires flags. Tests construct an isolated command per case and confirm flag state cannot leak between two instances of the same constructor.

The carve-out is `move.go:findActive`, a package-level seam swapping `lock.FindActive` for tests via `withMoveSeams` with `t.Cleanup` discipline. The seam is injection for an external dependency, not the per-cmd shared mutable state the constructor pattern targets.

## Category selection

`applyCategorySelection` (in `category_selection.go`) is the single owner of `--from-manifest` exclusivity with `--all` and per-category flags. Both `newExportCmd` and `newPushCmd` route their flag-to-`CategorySet` plus placeholder discovery through it. The helper reads `--from-manifest` and the category flags via `cmd.Flags()`, so callers do not need a closure-scoped `fromManifest` variable just to forward it.

When `--from-manifest` is set, the helper rejects `--all` or any per-category flag with one error message naming the conflicts. When the flag is empty, the helper falls through to `resolveCategoriesFromCmd`; a zero `CategorySet` triggers the interactive `ui.SelectCategories()` prompt.

`runExportManifest` is the only command body that does not call `applyCategorySelection`. It runs the same fall-through logic inline because it skips `export.Run` and the prompt path is the only one that applies (the manifest subcommand has no `--from-manifest` flag).

## Rules-warning routing

`renderRulesReport` (in `render.go`) is the single renderer for `~/.claude/rules/*.md` warnings. It consumes the `scan.Report` carried on `export.Result.RulesReport`, `move.Plan.RulesReport`, `importer.Result.RulesReport`, and the sync `Plan.RulesReport`. No cmd body calls `scan.Rules` directly. The sole legitimate inline-scan site is `runExportManifest`, which does not run `export.Run` and so has no `Result` to read the report from.

## Stream routing

Every cmd write goes through `cmd.OutOrStdout()` for normal output and `cmd.ErrOrStderr()` for warnings. The cobra streams let tests capture output with `cmd.SetOut` / `cmd.SetErr` per invocation. Bare `fmt.Printf`, `fmt.Println`, and direct `os.Stderr` writes are banned.

## Tests

`importcmd_test.go` in this package tests cobra wiring on the `import` and `import manifest` subcommands (passphrase flags, manifest output guard). `category_selection_test.go` pins the `--from-manifest` exclusivity rule across every per-category flag. Most behavioral tests live in the owning `internal/*` packages. Push and pull dispatch tests (`openPriorRead`, `openArchiveSource`) live alongside the cmd helpers because the dispatch is owned here. `integration_test.go` at the repo root runs full CLI end-to-end against a fixture `~/.claude`.
