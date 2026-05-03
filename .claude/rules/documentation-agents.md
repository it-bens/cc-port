---
paths:
  - "**/AGENTS.md"
---

# AGENTS.md Rules (cc-port)

**CRITICAL**: AGENTS.md is a pointer-only map into the adjacent README. No summaries; they drift. Every bullet is a pointer `(README §X)` or it gets deleted. Hard ceiling: 30 lines, 3 to 8 bullets.

## Decision Test (run before writing or editing an AGENTS.md bullet)

> **"Does this bullet end with `(README §<section name>)` pointing at a real heading, and does it avoid explaining *why*?"**

- Yes → proceed
- No pointer → delete the bullet, or rewrite as a pointer
- Explains motivation → move the motivation to the README; leave only the rule + pointer
- Pointer targets a vague or missing heading → fix the README heading first, then repoint

## Core Rules

- Every bullet under `## Before editing` MUST end with `(README §<section name>)`. No pointer → delete the bullet.
- No sentence explains *why*. Motivation belongs in the README; AGENTS.md only points at it.
- Front-load the highest-stakes invariant. The first bullet under `## Before editing` carries the most attention weight; order by stakes, not by source-file order.
- Cross-refs use `§<section name>`, never line numbers, never anchor links. Headings survive edits; line numbers don't.
- A module with no hard cross-cutting constraint gets **no** AGENTS.md. A ceremonial AGENTS.md is noise.
- 3 to 8 bullets. Twelve is a smell. Prose drift is the failure mode the 30-line ceiling prevents.

## Banned Patterns

```
WRONG:   - File-history snapshots are opaque because rewriting the
           binary payload would corrupt JPEG/WebP data, as we learned
           during the export work in Q2.
CORRECT: - File-history snapshots are opaque bytes. No module inspects
           or rewrites them (README §File-history policy).
```

## Red Flags

| Thought | Reality |
|---|---|
| "I'll add a one-line summary to AGENTS.md so readers don't need the README" | The summary will drift. The pointer is the discipline |
| "Every module should have an AGENTS.md for consistency" | Absence is a positive signal. An AGENTS.md with no hard rule is noise |
| "I'll stub an AGENTS.md with plausible wiring rules now" | Invented constraints are misinformation. Write the rule when real evidence surfaces |
| "I'll shorten `repository` to `repo` to tighten AGENTS.md" | Identifiers mirror code. AGENTS.md abbreviations that drift from the identifiers they reference defeat grep and break the pointer-to-README coupling |
