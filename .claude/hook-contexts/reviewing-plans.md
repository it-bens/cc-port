# cc-port project overlay for reviewing-plans

This overlay applies to plan reviews in the cc-port repository. It pre-resolves authorship stance, spec-lookup conventions, and invariant discovery (the workflow's deliberation on these is unnecessary here); narrows the scope-and-guardrail lens to project posture; and lists the writing skills to invoke before the four lenses are applied. Each section below is self-contained — apply each when its named workflow phase comes up.

## Treat the plan author as external

cc-port is vibe coded by a single maintainer with no external consumers (CLAUDE.md). When the workflow resolves authorship stance, use the no-stake stance — no courtesy, no defence, aggressive counter-evidence hunt — and do not call AskUserQuestion to confirm. The answer is fixed.

## Resolve specs from these paths

When the workflow locates the spec paired with a plan:

- Plans live in `docs/superpowers/plans/` and `docs/superpowers/plans/archive/`.
- Specs live in `docs/superpowers/specs/` and `docs/superpowers/specs/archive/`.
- Naming: `<plan-stem>-design.md` is the dominant pattern; some older archived pairs (e.g. `2026-04-21-architecture-refactor-smell-*`) drop the `-design` suffix.

## Treat these invariants as pre-discovered

Each invariant below is enforced at the cited site; root `AGENTS.md` carries the canonical list. When the workflow discovers invariants and module contracts, treat the named sites as the source of truth instead of re-deriving them. When the plan touches one of these invariants, the cited site is the authority.

- Path rewrites must route through `rewrite.ReplacePathInBytes`. Never `strings.ReplaceAll` on user paths. (`internal/rewrite/README.md` §Boundary rules)
- Mutating command bodies (`move --apply`, `import`) must wrap in `lock.WithLock` before any write. (`internal/lock/README.md` §Concurrency guard)
- Adversarial archive paths contained with `os.Root`; decompressed reads bounded by `io.LimitReader`. (`internal/importer/README.md` §Import contract)
- Archive cap-guard edits require `go test -tags large ./internal/importer/...` locally. (`internal/importer/README.md` §Tests)
- New `bufio.Scanner` over untrusted input requires an explicit `bufio.Scanner.Buffer` cap. (`internal/scan/README.md` §Rules files preserved)
- `bufio.Scanner` over `history.jsonl` must use `claude.MaxHistoryLine`. (`internal/claude/README.md` §History line cap)
- New session-keyed directories require entries in both `claude.SessionKeyedGroups` and `transport.SessionKeyedTargets`. (`internal/claude/README.md` §Session-keyed registry)
- Every export category must appear in `manifest.AllCategories`; no hard-coded parallel list. (`internal/manifest/README.md` §Category manifest)
- File-history snapshot contents are off-limits to inspection or rewrite. (`docs/architecture.md` §File-history policy)

Module layout for invariant lookups:

- CLI entry: `cmd/cc-port`.
- Modules: `internal/<name>`.
- Cross-module narrative and invariant-to-owner index: `docs/architecture.md`.
- Per-module agent rules: `internal/<name>/AGENTS.md`.
- Per-module narrative: `internal/<name>/README.md`.

## Consult `docs/design-rules.md` for task-keyed rules

`docs/design-rules.md` carries right-way / failure-modes pairings for tasks that don't condense to a single primitive — path-rewrite surface enumeration, opacity decisions, mutating command body shape, pipeline composition, host-system assumptions, shape extraction. Read it during invariant discovery; consult the matching entry whenever the plan proposes covered work.

## Load surface-specific writing skills before applying the lenses

Determine which writing skills apply from the plan's file plan: `software-writer:writing-code` for any `**/*.go`, `software-writer:writing-tests` for any `**/*_test.go`, `software-writer:writing-docs` for any `README.md`, `AGENTS.md`, or `docs/architecture.md`. Invoke each once before any plan content is evaluated against the four lenses; a plan editing twelve `*.go` files invokes `software-writer:writing-code` exactly once.

## Findings against cc-port's documented posture are block-class

The base scope-and-guardrail lens routes posture-silent projects to `awareness-single-option` or `multi-option`. cc-port is not posture-silent: CLAUDE.md commits to no BC promise, no extension-point budget, and no risk-mitigation budget. Findings against that posture are `block-class`, not `multi-option`. Specifically:

- Preservation mechanisms — compatibility shims, dual code paths, deprecation steps, "preserve current behavior" — for non-existent consumers → `block-class`. Documentation labels for breaks that did happen (`BREAKING CHANGE:` trailers, changelog entries, README notes about removed APIs) are not flagged; the flag is for preservation, not for labels.
- Plugin hooks, public registration APIs, "make this pluggable" scaffolding for non-existent extension authors → `block-class`. Internal abstractions that organize today's code are fine; the test is whether the surface exists to keep a non-existent consumer working.
- "Keep the diff small", "minimize blast radius", "stay focused" as scope justifications → `block-class`. cc-port optimizes every change for the cleanest end state, not the smallest diff.

Acceptable sources for the citable-source test on cc-port:

- A spec line naming the user's choice.
- `CLAUDE.md`, `AGENTS.md`, or `docs/architecture.md` posture.
- A prior commit.
- An active named follow-up plan that exists in `docs/superpowers/plans/`.

## Recommend the cleanest end state, not the smallest diff

When the workflow recommends an option, pick the option that produces the cleanest end state for cc-port. "Would touch fewer files", "would be safer to defer", "minimizes risk", and "keeps this PR small" are not valid recommendation reasons in this project — they map to budgets cc-port disclaims.

## Use this shape when recording a user-accepted violation

`Note: user accepted skipping lock.WithLock here on 2026-04-29; the wrapped call is read-only and cannot interleave with a writer.`
