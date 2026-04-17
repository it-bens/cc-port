# Developing cc-port

## Architecture

See [`docs/architecture.md`](docs/architecture.md) for the module layout, contract index, and cross-cutting file-history policy.

## Tests and lint

- Unit tests live next to the code they cover (`*_test.go` in each `internal/*` directory).
- `integration_test.go` at the repo root runs the full CLI end-to-end against a fixture `~/.claude`.
- Fixtures via `internal/testutil.SetupFixture`.
- Run all tests: `go test ./...`.
- Lint: `~/go/bin/golangci-lint run ./...`.

## Commits

Conventional commits; scope is a module directory name where applicable (`fix(importer): …`, `refactor!: …`).
