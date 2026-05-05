# AGENTS.md (reference)

Loaded from `writing-docs` SKILL.md when the workflow branches into the AGENTS.md surface — for both *Apply the surface shape* and *Write the content*.

AGENTS.md is a pointer-only map into the adjacent module README. No summaries; summaries drift. Every bullet is a pointer `(README §X)` or it gets deleted. Hard ceiling: 30 lines, 3 to 8 bullets per section.

## Existence check

A module gets an AGENTS.md only when it owns at least one hard cross-cutting constraint that an editor must know before changing code. Examples in the current cc-port tree: `internal/lock` (concurrency guard around mutating commands), `internal/importer` (`os.Root` and `io.LimitReader` containment), `internal/rewrite` (no `strings.ReplaceAll` on user paths).

A module without such a constraint gets **no** AGENTS.md. A ceremonial AGENTS.md is noise — the agent loads it expecting a warning and finds none, so the next AGENTS.md it loads carries less weight too.

## Skeleton

```markdown
# <module>: agent notes

<one short line of orientation; optional>

## Before editing

- <highest-stakes invariant in one line> (README §<heading>)
- <next invariant> (README §<heading>)
- ...

## Navigation

- <subdirectory or key file>: <one-line role>
- <subdirectory or key file>: <one-line role>
```

The `## Before editing` section carries the warnings. The `## Navigation` section is a flat map of where to find what — one line per entry, no nesting.

## Bullet discipline (`## Before editing`)

Every bullet ends with `(README §<heading name>)` pointing at a real heading in the adjacent README. The bullet states the rule; the README carries the *why*.

- **No motivation in the bullet.** Motivation lives in the README. AGENTS.md only points at it.
- **Front-load by stakes.** The first bullet under `## Before editing` carries the most attention weight. Order by stakes, not by source-file order.
- **Cross-refs use `§<heading name>`, never line numbers, never anchor links.** Headings survive edits; line numbers don't.
- **3 to 8 bullets per section.** Twelve is a smell. Prose drift is the failure mode the 30-line ceiling prevents.
- **Identifiers mirror the code.** Don't shorten `repository` to `repo` to tighten the bullet — abbreviations that drift from the identifiers they reference defeat grep and break the pointer-to-README coupling.

## Worked WRONG / CORRECT

```
WRONG:   - File-history snapshots are opaque because rewriting the
           binary payload would corrupt JPEG/WebP data, as we learned
           during the export work in Q2.
CORRECT: - File-history snapshots are opaque bytes. No module inspects
           or rewrites them (README §File-history policy).
```

The WRONG version explains the *why* (corruption risk, Q2 export work). That belongs in the README's File-history policy section. The bullet's job is to flag the rule and point.

```
WRONG:   - Lock contract: see internal/lock/README.md:42
CORRECT: - Wrap every mutating command body in lock.WithLock before any
           write. (README §Concurrency guard)
```

Line numbers shift the moment anyone reformats the file. Section names survive heading-internal edits and only break on a rename — at which point the cross-ref integrity gate in the SKILL.md catches the rename and forces the sweep.

## Decision Test (per bullet)

> Does this bullet end with `(README §<heading name>)` pointing at a real heading, and does it avoid explaining *why*?

- Yes → proceed.
- No pointer → delete the bullet, or rewrite as a pointer.
- Explains motivation → move the motivation to the README; leave only the rule + pointer.
- Pointer targets a vague or missing heading → fix the README heading first (heading-predicts-content discipline), then repoint.

## CLAUDE.md companion

Each module with an AGENTS.md has a `CLAUDE.md` containing the single line `@AGENTS.md`. CLAUDE.md is not authored prose; it is a one-line include directive that Claude Code resolves when entering the module. When you create a new AGENTS.md, create the adjacent CLAUDE.md in the same edit. When you delete an AGENTS.md, delete its CLAUDE.md.

## Common rationalizations to refuse

| Thought | Reality |
|---|---|
| "I'll add a one-line summary so readers don't need the README" | The summary will drift. The pointer is the discipline. |
| "Every module should have an AGENTS.md for consistency" | Absence is a positive signal. An AGENTS.md with no hard rule is noise. |
| "I'll stub an AGENTS.md with plausible wiring rules now" | Invented constraints are misinformation. Write the rule when real evidence surfaces. |
| "I'll shorten the identifier to tighten the bullet" | AGENTS.md abbreviations that drift from the identifiers defeat grep and break the pointer-to-README coupling. |
