# cc-port — agent notes

Go CLI that rewrites Claude Code project state (paths, indexes, archives).
See README.md for the project overview and contract index.

## Before editing anywhere

- File-history snapshots are opaque bytes — no module inspects or
  rewrites them (docs/architecture.md §File-history policy (cross-cutting)).
- Five `~/.claude/` directories beyond the project's encoded dir carry
  per-session state for the project (todos, usage-data/session-meta,
  usage-data/facets, plugins/data, tasks). All flow through `LocateProject`
  and the existing move/export/import paths. See
  [docs/architecture.md §Session-UUID-keyed user-wide data](docs/architecture.md).
- All path-substring rewrites route through
  `internal/rewrite.ReplacePathInBytes` — never hand-roll
  `strings.ReplaceAll` on user paths (see `internal/rewrite/README.md`).
- Mutating commands (`move --apply`, `import`) acquire
  `~/.claude/.cc-port.lock` + run the live-session check before any
  write (see `internal/lock/README.md`).
- Contract docs live in module `README.md` §Contracts. Module
  `AGENTS.md` files are pointer-only — if you catch yourself explaining
  *why* in an AGENTS.md, move it to the README.

## Navigation

- CLI entry: `cmd/cc-port`.
- Commands: `internal/move`, `internal/export`, `internal/importer`.
- Shared primitives: `internal/rewrite`, `internal/lock`,
  `internal/fsutil`, `internal/claude`, `internal/scan`, `internal/ui`.
- Each directory with a `README.md` has an `AGENTS.md` summarising its
  hard rules — read it before editing files in that directory.

## Tests

- Unit tests live next to code (`*_test.go`).
- `integration_test.go` at repo root runs the full CLI end-to-end.
- Fixtures via `internal/testutil`.
- Run: `go test ./...`.

## Commits

- Conventional commits; scope is a module directory name where applicable
  (`fix(importer): …`, `refactor!: …`).
