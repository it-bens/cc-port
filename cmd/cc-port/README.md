# cmd/cc-port

Wiring layer for the cc-port CLI.

## Purpose

This package owns flag parsing, stdout formatting, and exit-code mapping. Business logic lives in `internal/*`.

## Commands

- `move`: plans and applies a project path rename, printing a dry-run diff before any write.
- `export`: archives a project and its session-keyed data to a ZIP file, with an optional `manifest` subcommand to write a standalone XML for hand-editing.
- `import`: restores a project from a ZIP archive into a target path, resolving path placeholders interactively or via `--from-manifest` / `--resolution` flags.
- `push`: uploads a project archive to a remote under a stable name, with cross-machine conflict refusal overridable by `--force`.
- `pull`: downloads a named archive from a remote and applies it to a target path, sharing the placeholder-resolution contract with `import`.

See the root `README.md` §Commands for one-line syntax and worked examples. Run `cc-port <subcommand> --help` for the full flag reference.

## Tests

`importcmd_test.go` in this package tests `parseResolutionFlags` flag parsing and validation. Most behavioral tests live in the owning `internal/*` packages. Push and pull dispatch tests (`openPriorRead`, `openArchiveSource`) live alongside the cmd helpers because the dispatch is owned here. `integration_test.go` at the repo root runs full CLI end-to-end against a fixture `~/.claude`.
