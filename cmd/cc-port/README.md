# cmd/cc-port

## Purpose

The CLI entry point. `main.go` wires the root command and the `--claude-dir` override; `move.go`, `export.go`, and `importcmd.go` are the subcommand wrappers. Every subcommand delegates to its `internal/*` package — the wiring layer owns flag parsing, stdout formatting, and exit-code mapping, not business logic.

## Public API

The package exposes nothing; it is a `main` package. Its surface is the CLI:

- `cc-port move <old-path> <new-path> [--apply] [--refs-only] [--rewrite-transcripts]`
- `cc-port export <project-path> --output <archive.zip>` and `cc-port export manifest <project-path> [--output <manifest.xml>]`
- `cc-port import <archive.zip> <target-path>` and `cc-port import manifest <archive.zip>`

See the root `README.md` §Commands for one-line syntax + a worked example per subcommand, and `cc-port <subcommand> --help` for the full flag reference.

## Tests

`integration_test.go` at the repo root runs the full CLI end-to-end against a fixture `~/.claude`. Per-subcommand unit tests live under the owning `internal/*` package.
