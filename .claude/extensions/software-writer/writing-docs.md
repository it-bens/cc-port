## Named-value assignments

- `docs.surfaces` =
  | Surface | Owns | Shape | Single-owner |
  |---|---|---|---|
  | Root `README.md` | pitch, install, command tour, development pointer, license | delegates deeper structure to `DEVELOPMENT.md` and `docs/architecture.md`; never re-explains module contracts | enforced |
  | `DEVELOPMENT.md` | dev setup, Claude Code plugin notes, test and lint commands, commit conventions, release process | task-oriented sections | enforced |
  | Module `README.md` (`internal/*`, `internal/tool/*`, `cmd/cc-port`, `dev/s3`) | the module's purpose, public API, contracts, quirks, tests | Purpose / Public API / Contracts / Quirks / Tests; short shape when the module owns no invariant | enforced |
  | `docs/architecture.md` | cross-module narrative, invariant-to-owner index, cross-cutting policies, crash and idempotence contract; jargon home | layout tree + tool-contract narrative + index rows as links + `(cross-cutting)`-tagged sections | enforced |
  | Module `AGENTS.md` + root `AGENTS.md` | pointer map into the adjacent README (root: cross-module map; bullets cite file + heading) | pointer skeleton, ≤30 lines | enforced (pointer-only) |
  | `CLAUDE.md` companions | nothing — a one-line `@AGENTS.md` include; the root `CLAUDE.md` additionally carries the skill-invocation table | single line (root: line + table) | exempt (structural include) |
  | `docs/release-checklist.md` | the manual release-regression runbook | ordered per-command checklist sections | enforced |
- `docs.pointer_file` = `AGENTS.md` — AGENTS.md owns pointer content; every AGENTS.md has a companion `CLAUDE.md` containing only `@AGENTS.md`, created and deleted as a pair.

## Pre-Step-1

Procedural artifacts under `docs/superpowers/` (plans and specs) are not documentation surfaces. Do not edit them with this skill and never register or cross-reference them from a registered surface.
