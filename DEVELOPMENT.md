# Developing cc-port

## Architecture

See [`docs/architecture.md`](docs/architecture.md) for the module layout, contract index, and cross-cutting file-history policy.

## Codex reference submodule

`.reference/codex` is the Codex upstream-source submodule (docs/architecture.md §Codex upstream reference (cross-cutting)).

A plain `git clone` leaves it empty. Populate it after cloning:

```
git submodule update --init .reference/codex
```

Bump it to a newer Codex release by moving the pin to the tag and committing the updated gitlink:

```
git -C .reference/codex fetch --tags --depth 1
git -C .reference/codex checkout <rust-vX.Y.Z>
git add .reference/codex
```

`git submodule status` prints the pinned tag.

### ChunkHound index

Agents consult the submodule through a read-only ChunkHound MCP
(`chunkhound-codex`) over a local index. Build or refresh that index from the
repo root:

```
chunkhound index --config .reference/.chunkhound-codex.json .reference/codex
```

The index at `.reference/.chunkhound-codex/` is gitignored. Only its config is
committed. Rebuild it after bumping the submodule so it matches the pinned tag,
then restart the codex MCP client to serve the refreshed index.

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

## Local S3 backend

`dev/s3/` provides a project-stored Garage container exposing an S3-compatible API on `http://localhost:9000`. Used by the demo videos and (planned) E2E tests of `internal/sync` / `internal/remote`.

```
make s3-up      # Start
make s3-down    # Stop
make s3-reset   # Destroy and recreate
```

See [`dev/s3/README.md`](dev/s3/README.md) for credentials, endpoint conventions, and rationale.

## Demo videos

`make videos` re-renders the GIF and MP4 clips in `docs/images/` from `docs/videos/*.tape`. It resets the local S3 backend to an empty bucket before rendering — dropping any data already there — and stops it afterward.

It requires `vhs`, Docker, and `codex-cli 0.145.0`, and needs no Codex account. Docker hosts the S3 backend the push/pull clip uses. The paired clips run a keyless `codex` command to rebuild Codex's session index from the imported sessions. With no account configured its model turn cannot complete, but the local reindex commits first, which the closing `cc-port stats` shows.

The render pins that Codex version. When the system `codex` is a newer build, download `codex-cli 0.145.0` from the [Codex releases](https://github.com/openai/codex/releases) and pass its path: `make videos CODEX=/path/to/codex`. The download stays outside the repo and is never committed.

## Commits

Conventional commits; scope is a module directory name where applicable (`fix(importer): …`, `refactor!: …`).

## Releases

Push a tag matching `v*` (e.g. `v0.1.0`) to trigger a release. The `.github/workflows/release.yml` workflow runs goreleaser, which builds binaries for macOS and Linux on amd64 and arm64, creates a GitHub release with tarballs and checksums, and pushes a cask update to [`it-bens/homebrew-tap`](https://github.com/it-bens/homebrew-tap). The cask is macOS-only; Linux users install via `go install` or the tarballs.

Prerequisite: the `HOMEBREW_TAP_GITHUB_TOKEN` repository secret must be set to a fine-grained PAT with `contents: write` on `it-bens/homebrew-tap`. Without it, the cc-port release still succeeds but the tap push fails.

Local snapshot build (no publish): `make snapshot`.
