# cc-port — agent notes

Go CLI that rewrites Claude Code project state (paths, indexes, archives).
See README.md for the project overview and contract index.

## Before editing anywhere

- File-history snapshots are opaque bytes — no module inspects or
  rewrites them (docs/architecture.md §File-history policy (cross-cutting)).
- Five `~/.claude/` directories beyond the project's encoded dir carry
  per-session state for the project (todos, usage-data/session-meta,
  usage-data/facets, plugins-data, tasks). All flow through `LocateProject`
  and the existing move/export/import paths. See
  [docs/architecture.md §Session-UUID-keyed user-wide data](docs/architecture.md).
- The five session-keyed directories are enumerated by
  `claude.SessionKeyedGroups`; archive layout lives in
  `transport.SessionKeyedTargets`. Adding a sixth group means appending
  one entry to each slice — `move.planCategories` is derived from
  `claude.SessionKeyedGroups` at package init, so it picks the new
  entry up for free. The alignment test in `internal/transport`
  catches drift between the two slices.
- The nine export categories live in one place: `manifest.AllCategories`.
  `manifest.BuildCategoryEntries` writes the `metadata.xml` list and
  `manifest.ApplyCategoryEntries` is the only path that validates a read
  manifest (every missing or unknown name hard-fails via
  `errors.Join`). See
  [internal/manifest/README.md §Category manifest](internal/manifest/README.md).
- All path-substring rewrites route through
  `internal/rewrite.ReplacePathInBytes` — never hand-roll
  `strings.ReplaceAll` on user paths (see `internal/rewrite/README.md`).
  **Exception (discouraged):** placeholder-token substitution in
  `internal/importer.ResolvePlaceholders`, where the `{{KEY}}` shape is
  self-delimiting and boundary-awareness would be actively wrong. Before
  adding another exception, re-read that godoc and confirm the token
  shape provides an equivalent self-delimiting guarantee; otherwise, add
  the rewrite to `internal/rewrite` instead.
- Mutating commands (`move --apply`, `import`) wrap their work in
  `lock.WithLock` before any write (see `internal/lock/README.md`).
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
- `integration_test.go` at repo root runs the full CLI end-to-end; gated
  behind `//go:build integration` and excluded from a plain `go test ./...`.
- Fixtures via `internal/testutil`.
- Run unit: `go test ./...`. Run unit + integration:
  `go test -tags integration ./...`.

## Commits

- Conventional commits; scope is a module directory name where applicable
  (`fix(importer): …`, `refactor!: …`).
