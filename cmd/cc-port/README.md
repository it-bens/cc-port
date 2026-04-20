# cmd/cc-port

Wiring layer for the cc-port CLI.

## Purpose

This package owns flag parsing, stdout formatting, and exit-code mapping. Business logic lives in `internal/*`.

## Commands

- `move`: plans and applies a project path rename, printing a dry-run diff before any write.
- `export`: archives a project and its session-keyed data to a ZIP file, with an optional `manifest` subcommand to write a standalone XML for hand-editing.
- `import`: restores a project from a ZIP archive into a target path, resolving path placeholders interactively or via `--from-manifest` / `--resolution` flags.

See the root `README.md` §Commands for one-line syntax and worked examples. Run `cc-port <subcommand> --help` for the full flag reference.

## Tests

`importcmd_test.go` in this package tests `parseResolutionFlags` flag parsing and validation. Per-subcommand behavioral tests live in the owning `internal/*` packages. `integration_test.go` at the repo root runs full CLI end-to-end against a fixture `~/.claude`.
