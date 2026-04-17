# cc-port

`cc-port` rewrites Claude Code project state after a rename, an export, or an import. When you move a project directory on disk or hand it to a teammate, the absolute paths baked into `~/.claude/projects/<encoded>/`, `~/.claude/history.jsonl`, `~/.claude.json`, and `~/.claude/file-history/<uuid>/` no longer match. cc-port rewrites the references safely — boundary-aware substring replacement, atomic writes with rollback, and a lock-plus-live-session check so no operation collides with a running Claude Code process.

## Install

Homebrew (this repo's tap):

```
brew install it-bens/tap/cc-port
```

Or with `go install`:

```
go install github.com/it-bens/cc-port/cmd/cc-port@latest
```

Prebuilt releases (macOS / Linux tarballs, checksums) are published under [GitHub Releases](https://github.com/it-bens/cc-port/releases).

## Commands

Full flag reference: `cc-port <subcommand> --help`.

- `cc-port move <old-path> <new-path> [--apply]` — rewrite every reference to `<old-path>` under `~/.claude/` to `<new-path>`. Default is dry-run; `--apply` copies, verifies, then deletes the old encoded directory.

  ```
  cc-port move /Users/me/old-project /Users/me/new-project --apply
  ```

- `cc-port export <project-path> <archive.zip>` — produce a portable archive of one project. Use `--all`, `--sessions`, `--memory`, `--history`, `--file-history`, `--config` to select categories; omit all flags for an interactive picker.

  ```
  cc-port export /Users/me/project /tmp/project.zip --all
  ```

- `cc-port export manifest <project-path> <manifest.xml>` — emit only the manifest for review / editing, then feed it back via `--from-manifest` on a subsequent `export` or `import`.

  ```
  cc-port export manifest /Users/me/project /tmp/project.xml
  ```

- `cc-port import <archive.zip> <project-path>` — apply an archive to `<project-path>`. Placeholder resolutions come from `--resolution KEY=VALUE` flags or from a manifest via `--from-manifest`.

  ```
  cc-port import /tmp/project.zip /Users/teammate/project
  ```

## Architecture

```
cc-port/
├── cmd/cc-port/            CLI entry point (flag parsing, dispatch, exit codes)
├── internal/
│   ├── claude/             Claude Code data layout: path encoding, locations, schemas
│   ├── export/             Export orchestration: ZIP, manifest, path anonymisation
│   ├── fsutil/             Shared filesystem helper (directory copy)
│   ├── importer/           Import orchestration: placeholder validation, atomic staging
│   ├── lock/               Advisory lock + live-session refusal
│   ├── move/               Move plan, dry-run, apply with copy-verify-delete
│   ├── rewrite/            Byte-level rewrite primitives + SafeRenamePromoter
│   ├── scan/               Read-only scanner for ~/.claude/rules/*.md
│   ├── testutil/           Test fixture helper
│   └── ui/                 Interactive prompts (charm.land/huh v2)
├── integration_test.go     End-to-end CLI tests
└── testdata/dotclaude/     Minimal ~/.claude fixture for tests
```

Each non-trivial directory has a `README.md`; directories with hard editing rules additionally carry an `AGENTS.md` (loaded by Claude Code via a one-line `CLAUDE.md`). The `README.md` is the developer narrative; the `AGENTS.md` is a pointer-only map into it.

## Contracts

One invariant per row; click through to the owning module for the full `Handled / Refused / Not covered` breakdown.

| Invariant | Owner |
| --- | --- |
| Interactive prompts require a TTY | [`internal/ui/README.md`](internal/ui/README.md) |
| Path substring rewrites respect component boundaries | [`internal/rewrite/README.md`](internal/rewrite/README.md) |
| Project paths use a lossy encoding; collisions refused | [`internal/claude/README.md`](internal/claude/README.md) |
| `~/.claude/rules/*.md` never rewritten in place | [`internal/scan/README.md`](internal/scan/README.md) |
| Malformed `history.jsonl` lines preserved, not repaired | [`internal/move/README.md`](internal/move/README.md) |
| Archives are a closed placeholder contract | [`internal/importer/README.md`](internal/importer/README.md) §Import contract |
| Import writes are atomic with rollback | [`internal/importer/README.md`](internal/importer/README.md) §Atomic staging |
| Mutating commands lock + refuse during live sessions | [`internal/lock/README.md`](internal/lock/README.md) |

## File-history policy (cross-cutting)

cc-port treats every file under `~/.claude/file-history/<session-uuid>/`
as an opaque byte stream. The directory is indexed by session UUID (not
by project path), and each `<hash>@vN` entry is a verbatim copy of a
file the user edited through Claude Code — the in-session rewind feature
uses it by filename, not by content. Any project-path string that
appears inside a snapshot body is coincidental (log line, comment,
string literal) and not load-bearing, so cc-port never inspects or
rewrites snapshot contents.

Per-command handling:

- [`internal/move/README.md`](internal/move/README.md) §File-history handling (move) — copy-verbatim, stderr warning, stale-path-strings residual risk.
- [`internal/export/README.md`](internal/export/README.md) §File-history handling (export) — archive-verbatim, stderr warning, privacy-of-exported-snapshots residual risk and the `--file-history=false` opt-out.
- [`internal/importer/README.md`](internal/importer/README.md) §File-history handling (import) — write-verbatim, `ResolvePlaceholders` no-op detail on current archives.

## Development

- Unit tests live next to the code they cover (`*_test.go` in each `internal/*` directory).
- `integration_test.go` at the repo root runs the full CLI end-to-end against a fixture `~/.claude`.
- Fixtures via `internal/testutil.SetupFixture`.
- Run all tests: `go test ./...`.
- Lint: `~/go/bin/golangci-lint run ./...`.
- Design specs under `docs/superpowers/specs/`; implementation plans under `docs/superpowers/plans/`.
- Conventional commits; scope is a module directory name where applicable (`fix(importer): …`, `refactor!: …`).

## License

See [`LICENSE`](LICENSE).
