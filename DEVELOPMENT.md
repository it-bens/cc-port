# Developing cc-port

## Architecture

See [`docs/architecture.md`](docs/architecture.md) for the module layout, contract index, and cross-cutting file-history policy.

## Claude Code plugins

Add the marketplaces ([`it-bens/ai-tools`](https://github.com/it-bens/ai-tools) and [`shopwareLabs/ai-coding-tools`](https://github.com/shopwareLabs/ai-coding-tools)):

```
/plugin marketplace add it-bens/ai-tools
/plugin marketplace add shopwareLabs/ai-coding-tools
```

[`superpowers-additions@itb-ai-tools`](https://github.com/it-bens/ai-tools/tree/main/plugins/superpowers-additions) is required, on top of the default superpowers plugin:

```
/plugin install superpowers-additions@itb-ai-tools
```

[`reviewing-plans-with-opus-enforcer@itb-ai-tools`](https://github.com/it-bens/ai-tools/tree/main/plugins/reviewing-plans-with-opus-enforcer) is optional:

```
/plugin install reviewing-plans-with-opus-enforcer@itb-ai-tools
```

Other recommended plugins:

- [`explore-with-sonnet-enforcer@itb-ai-tools`](https://github.com/it-bens/ai-tools/tree/main/plugins/explore-with-sonnet-enforcer)
- [`native-tools-enforcer@itb-ai-tools`](https://github.com/it-bens/ai-tools/tree/main/plugins/native-tools-enforcer)
- [`plan-with-opus-enforcer@itb-ai-tools`](https://github.com/it-bens/ai-tools/tree/main/plugins/plan-with-opus-enforcer)
- [`redundant-read-blocker@itb-ai-tools`](https://github.com/it-bens/ai-tools/tree/main/plugins/redundant-read-blocker)
- [`gh-tooling@shopware-ai-coding-tools`](https://github.com/shopwareLabs/ai-coding-tools/tree/main/plugins/gh-tooling)

```
/plugin install explore-with-sonnet-enforcer@itb-ai-tools
/plugin install native-tools-enforcer@itb-ai-tools
/plugin install plan-with-opus-enforcer@itb-ai-tools
/plugin install redundant-read-blocker@itb-ai-tools
/plugin install gh-tooling@shopware-ai-coding-tools
```

## Tests and lint

- Unit tests live next to the code they cover (`*_test.go` in each `internal/*` directory).
- `integration_test.go` at the repo root runs the full CLI end-to-end against a fixture `~/.claude`. It is gated by a `//go:build integration` tag, so it is excluded from a plain `go test ./...` run.
- Fixtures via `internal/testutil.SetupFixture`.
- Run the full suite (unit + integration, expected before any commit): `go test -tags integration ./...`.
- Run unit tests only (fast iteration): `go test ./...`.
- Lint: `~/go/bin/golangci-lint run ./...`.
- Fuzz targets in `internal/rewrite/rewrite_fuzz_test.go` and `internal/importer/resolve_fuzz_test.go`. Seeds replay as deterministic subtests under `go test ./...`. The unbounded mutation loop is local-only, one target per run: `go test ./internal/rewrite -run=^$ -fuzz=^FuzzReplacePathInBytes$ -fuzztime=2m`. Commit any `testdata/fuzz/FuzzXxx/<hash>` file produced by a real regression so every future run replays it.

## Commits

Conventional commits; scope is a module directory name where applicable (`fix(importer): …`, `refactor!: …`).

## Releases

Push a tag matching `v*` (e.g. `v0.1.0`) to trigger a release. The `.github/workflows/release.yml` workflow runs goreleaser, which builds binaries for macOS and Linux on amd64 and arm64, creates a GitHub release with tarballs and checksums, and pushes a cask update to [`it-bens/homebrew-tap`](https://github.com/it-bens/homebrew-tap). The cask is macOS-only; Linux users install via `go install` or the tarballs.

Prerequisite: the `HOMEBREW_TAP_GITHUB_TOKEN` repository secret must be set to a fine-grained PAT with `contents: write` on `it-bens/homebrew-tap`. Without it, the cc-port release still succeeds but the tap push fails.

Local snapshot build (no publish): `make snapshot`.
