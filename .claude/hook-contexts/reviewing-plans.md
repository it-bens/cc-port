# cc-port project overlay for reviewing-plans

Project-specific context for the generalized `superpowers-additions:reviewing-plans` skill. Pre-resolves the discovery steps the plugin defers to runtime, names the cc-port invariants, and pins the cc-port posture so Step 4 §Scope and guardrail skepticism finds the right ladder rung.

## Step 1a: Authorship stance is pre-resolved

cc-port is vibe coded by a single maintainer with no external consumers (CLAUDE.md). The user did not author the plan and has no inherent stake in defending what it says. Use the **no-stake stance**: no courtesy, no defence, hunt for counter-evidence aggressively. Skip the AskUserQuestion prompt in Step 1a — the answer is fixed.

## Step 1b: Spec lookup conventions

- Plans: `docs/superpowers/plans/` and `docs/superpowers/plans/archive/`.
- Specs: `docs/superpowers/specs/` and `docs/superpowers/specs/archive/`.
- Naming: `<plan-stem>-design.md` is the dominant pattern; some older archived pairs (e.g. `2026-04-21-architecture-refactor-smell-*`) drop the `-design` suffix.
- Example plan path: `docs/superpowers/plans/2026-04-23-move-plugin-path-handling.md`.

## Step 3: Pre-discovered invariants

Each invariant is enforced at the cited site; root `AGENTS.md` carries the canonical list. When the plan touches one of these, treat the named site as the source of truth.

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

## Step 4 §Scope and guardrail skepticism: cc-port posture is non-negotiable

CLAUDE.md commits cc-port to no BC promise, no extension-point budget, and no risk-mitigation budget. The plugin skill's posture-silent edge case does not apply here; findings against this posture are `block-class`, not `multi-option`.

- Preservation mechanisms — compatibility shims, dual code paths, deprecation steps, "preserve current behavior" — for non-existent consumers → `block-class`. Documentation labels for breaks that did happen (`BREAKING CHANGE:` trailers, changelog entries, README notes about removed APIs) are not flagged; the flag is for preservation, not for labels.
- Plugin hooks, public registration APIs, "make this pluggable" scaffolding for non-existent extension authors → `block-class`. Internal abstractions that organize today's code are fine; the test is whether the surface exists to keep a non-existent consumer working.
- "Keep the diff small", "minimize blast radius", "stay focused" as scope justifications → `block-class`. cc-port optimizes every change for the cleanest end state, not the smallest diff.

Acceptable sources for the citable-source test on cc-port:

- A spec line naming the user's choice.
- `CLAUDE.md`, `AGENTS.md`, or `docs/architecture.md` posture.
- A prior commit.
- An active named follow-up plan that exists in `docs/superpowers/plans/`.

## Step 7: Recommendation criteria

Recommend the option that produces the cleanest end state for cc-port. "Would touch fewer files", "would be safer to defer", "minimizes risk", and "keeps this PR small" are not valid recommendation reasons in this project — they map to budgets cc-port disclaims.

## Step 8: cc-port-flavored documented-decision example

Use this shape when recording a user-accepted violation in the plan:

`Note: user accepted skipping lock.WithLock here on 2026-04-29; the wrapped call is read-only and cannot interleave with a writer.`
