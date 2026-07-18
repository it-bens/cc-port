# cmd/cc-port

Wiring layer for the cc-port CLI.

## Purpose

This package owns flag parsing, stdout formatting, and exit-code mapping.
Business logic lives in `internal/*`; the tool registry itself lives here
too, since only `cmd/cc-port` is allowed to import an adapter package (see
`docs/architecture.md` §The tool contract).

## Commands

- `move`: plans and applies a project path rename across every selected
  tool, printing a dry-run diff before any write.
- `export`: archives a project's data across every selected tool to a ZIP
  file, with an optional `manifest` subcommand to write a standalone XML for
  hand-editing.
- `import`: restores a project from a ZIP archive into a target path across
  every selected tool, resolving non-implicit placeholders via
  `--from-manifest`.
- `push`: uploads a project archive to a remote under a stable name, with
  cross-machine conflict refusal overridable by `--force`.
- `pull`: downloads a named archive from a remote and applies it to a target
  path, sharing the placeholder-resolution contract with `import`.
- `stats`: reports one project's footprint across every selected tool
  (per-surface path-reference counts and per-category disk usage), or with
  no argument ranks every known project by disk footprint. Read-only, no
  lock; the root `--json` flag switches the result to a DTO.

The `push` and `pull` subcommands accept `--credentials-file` (path to a
`.env`-style AWS credentials file) and `--no-prompt` (disable the
interactive prompt fallback). `credentials.Resolve` layers a file, then
environment variables, then an interactive prompt (see
[`internal/credentials/README.md`](../../internal/credentials/README.md)
§Source layering and precedence).

See the root `README.md` §Commands for one-line syntax and worked examples.
Run `cc-port <subcommand> --help` for the full flag reference.

## Tool registry and target resolution

`tools.go:newToolSet` is the composition root (see `docs/architecture.md`
§Registry: a literal constructor call, not a plugin system for the
constructor call itself and `NewSet`'s validation). `main.go:newRootCmd`
builds the set once and calls
`toolselect.go:registerToolFlags`, which registers the repeatable `--tool`
flag plus one generated `--<name>-home` flag per registered tool on the
root command's persistent flags. Every subcommand's `RunE` calls
`toolselect.go:resolveTargets` at call time, after cobra has parsed flags,
to turn the parsed selection into an opened `[]tool.Target`: the tools named
by `--tool` (each must be registered), or every `tool.Set.Detected()` tool
when `--tool` was not given at all. A `--<name>-home` override for a tool not
selected this run is a flag-parse-time error.

## Constructor pattern

Every cobra command is a constructor taking the tool registry and flags as
its first arguments: `newXCmd(toolSet *tool.Set, flags *toolFlags) *cobra.Command`
for commands that don't reach the interactive picker (`newMoveCmd`,
`newImportCmd`, `newPullCmd`, `newStatsCmd`), or
`newXCmd(toolSet *tool.Set, flags *toolFlags, banner Banner) *cobra.Command`
for the picker-reaching surfaces (`newExportCmd`, `newPushCmd`).
`newExportManifestCmd(toolSet *tool.Set, flags *toolFlags, banner ui.Banner) *cobra.Command`
also reaches the picker through `resolveCategoriesAndPlaceholders`, but only
needs the narrower `ui.Banner` that embeds, not the `RenderBeside`/`BesideString`
pair the manifest subcommand never calls. `newRootCmd(banner Banner) *cobra.Command`
builds `toolSet` and `flags` itself via `newToolSet` and `registerToolFlags`,
then threads both (and the banner, where applicable) into each subcommand. Other
flag-value locals live as closure variables inside each constructor body,
never as package-level `var`. No `init()` block wires flags (see §Constructor
isolation).

## Category selection

`applyCategorySelection` (in `category_selection.go`) is the single owner of
`--from-manifest` exclusivity with `--all` and `--include`. Both
`newExportCmd` and `newPushCmd` route their flag-to-selection plus
placeholder discovery through it. The helper reads `--from-manifest`, `--all`,
and `--include` via `cmd.Flags()`, so callers do not need a closure-scoped
`fromManifest` variable just to forward it.

When `--from-manifest` is set, `applyCategorySelection` rejects `--all` or
`--include` with one error message naming the conflicts, then builds the
per-tool selection and placeholders from the manifest's own `<tool>` blocks
via `categoriesAndPlaceholdersFromManifest`. When the flag is empty, it
delegates to `resolveCategoriesAndPlaceholders`, which runs
`resolveSelectionFromCmd` (`--all`/`--include`, both parsed via
`tool.ParseQualified` for the `<tool>/<category>` grammar) and falls through
to the interactive `ui.SelectCategories(banner, tools)` prompt when neither
flag is set, then discovers placeholders per target via
`Workspace.Placeholders`.

`runExportManifest` calls `resolveCategoriesAndPlaceholders` directly (it
skips `applyCategorySelection` because the manifest subcommand registers no
`--from-manifest` flag, so the exclusivity branch is dead code on that
path). One helper backs both surfaces.

## Banner DI

`bannerImpl` is a build-tag-selected unexported package var declared in
`banner_default.go` (`//go:build !logo`, set to `noopBanner{}`) and
`banner_logo.go` (`//go:build logo`, set to `logo.Banner{}`). `main()` reads
it once and threads it through `newRootCmd(banner)`. The default `cc-port`
binary embeds `noopBanner` (writes nothing); the `cc-port-with-logo` binary
embeds `logo.Banner` (renders the gantry-crane logo). No runtime flag
selects between the two; the choice is made by the build tag at the
composition root.

`Banner` is declared in `banner.go` as the cmd-local interface, embedding
`ui.Banner` and adding `RenderBeside` and `BesideString`. The embedding lets
the same banner value pass to `ui.SelectCategories(banner, tools)` without
losing `Render` through interface narrowing. `noopBanner` satisfies both
interfaces structurally.

## Warning routing

Per-tool warnings (rules-file matches surfaced by the Claude adapter's
export, residual-content notices from a move or import) travel as plain
`[]string` fields on the generic result types
(`tool.ExportResult.Warnings`, `move.ToolResult.Warnings`,
`importer.Result.Warnings`) rather than a dedicated report type. Each command
renders its own warnings after the run: `export.go:renderToolWarnings`,
`move.go:renderApplyResult`, `importcmd.go:renderImportWarnings`. Every
renderer prefixes a warning with the tool's `DisplayName` only when more
than one target ran this invocation.

## Stream routing

Every cmd write goes through `cmd.OutOrStdout()` for normal output and
`cmd.ErrOrStderr()` for warnings. The cobra streams let tests capture output
with `cmd.SetOut` / `cmd.SetErr` per invocation. Bare `fmt.Printf`,
`fmt.Println`, and direct `os.Stderr` writes are banned.

## Contracts

### Constructor isolation

#### Handled

- Two instances of the same `newXCmd` constructor never share flag state.
  `TestCommandConstructorsAreIsolated` (`cmd_isolation_test.go`) sets a flag
  on one instance of `newExportCmd`, `newImportCmd`, `newPushCmd`,
  `newPullCmd`, or `newMoveCmd` and asserts the second instance still reads
  the flag's zero value, which would fail if a package-level flag `var`
  were reintroduced.

#### Refused

- A package-level flag `var` anywhere in this package. Flag-value locals are
  closure variables scoped to one constructor call; no `init()` block wires
  a flag either.

#### Not covered

- `newStatsCmd` and `newExportManifestCmd` are not in
  `TestCommandConstructorsAreIsolated`'s case table, so the isolation
  guarantee for those two constructors rests on the same closure-variable
  pattern, not a dedicated regression test.

## Tests

`importcmd_test.go` in this package tests cobra wiring on the `import` and
`import manifest` subcommands (passphrase flags, manifest output guard).
`category_selection_test.go` pins the `--from-manifest` exclusivity rule
across `--all` and `--include`. `toolselect_test.go` pins `resolveTargets`'s
sweep semantics: an explicitly selected but undetected tool hard-fails with
`tool.ErrToolAbsent`, while a default sweep silently skips it. Most
behavioral tests live in the
owning `internal/*` and `internal/tool/*` packages. Push and pull dispatch
tests (`openPriorRead`, `openArchiveSource`) live alongside the cmd helpers
because the dispatch is owned here. `stats_test.go` pins the stats result
stream routing and the `--json` DTO shape; `stats_integration_test.go`
drives both stats modes end-to-end over the fixture. `integration_test.go`
at the repo root runs full CLI end-to-end against a fixture `~/.claude`.
