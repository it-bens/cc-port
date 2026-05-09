# cc-port project overlay for brainstorming

Project-specific context for the generalized `superpowers:brainstorming` skill. Adds a design-rules compliance pass to spec self-review so cc-port's task-keyed right-way rules are not silently violated by a fresh spec.

## Step 7: Spec self-review — design-rules pass

After the standard self-review checks (placeholder, internal consistency, scope, ambiguity), run a fifth pass against `docs/design-rules.md` before the User Review Gate.

`docs/design-rules.md` is task-keyed: each section pairs a task ("Substitute one project path for another in user-owned data", "Write a command body that mutates user state", "Add a category, session-keyed directory, or user-wide rewrite target", ...) with the way that holds in cc-port and the failure modes the right way avoids. A spec that proposes covered work without naming the right way silently re-introduces the failure mode at implementation time.

Procedure:

1. Read `docs/design-rules.md` end to end. Section headings name the tasks; scan them once to know what coverage exists.
2. For every section of the spec that proposes new work, identify the design-rules entries that cover it. A spec touching multiple subsystems can match multiple entries.
3. For each match, verify the spec describes the right way — named primitives, registries, staging mechanism, opacity policy. A spec proposing "rewrite paths in transcripts" without naming `rewrite.ReplacePathInBytes` (or the JSON-escape variant) is missing a covered invariant.
4. Failure-mode patterns are the inverse signal: a spec calling for `strings.ReplaceAll` on user paths, a hard-coded category enum that duplicates `manifest.AllCategories`, or a mutating command body that skips `lock.WithLock` is naming the failure mode directly. Treat each as a violation.
5. Fix violations inline. Do not defer to the implementation plan or to implementation itself.

Specs proposing work not covered by `docs/design-rules.md` still inherit the structural invariants in root `AGENTS.md` and per-module `internal/<name>/AGENTS.md` — those cover the rules that condense to a single primitive (explicit `bufio.Scanner.Buffer` cap, `claude.MaxHistoryLine`, ...).
