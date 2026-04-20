# Developing cc-port

## Architecture

See [`docs/architecture.md`](docs/architecture.md) for the module layout, contract index, and cross-cutting file-history policy.

## Tests and lint

- Unit tests live next to the code they cover (`*_test.go` in each `internal/*` directory).
- `integration_test.go` at the repo root runs the full CLI end-to-end against a fixture `~/.claude`. It is gated by a `//go:build integration` tag, so it is excluded from a plain `go test ./...` run.
- Fixtures via `internal/testutil.SetupFixture`.
- Run unit tests: `go test ./...`.
- Run unit + integration: `go test -tags integration ./...`.
- Lint: `~/go/bin/golangci-lint run ./...`.
- Fuzz targets in `internal/rewrite/rewrite_fuzz_test.go` and `internal/importer/resolve_fuzz_test.go`. Seeds replay as deterministic subtests under `go test ./...`; the unbounded mutation loop is local-only, one target per run, e.g. `go test ./internal/rewrite -run=^$ -fuzz=^FuzzReplacePathInBytes$ -fuzztime=2m`. Commit any `testdata/fuzz/FuzzXxx/<hash>` file produced by a real regression so every future run replays it.

## Commits

Conventional commits; scope is a module directory name where applicable (`fix(importer): …`, `refactor!: …`).
