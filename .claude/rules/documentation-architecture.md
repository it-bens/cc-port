# Documentation Architecture (cc-port)

**CRITICAL**: cc-port documentation has five single-purpose surfaces: code comments, module `README.md`, module `AGENTS.md`, `docs/architecture.md`, root `README.md`. No sentence appears on more than one. AGENTS.md is pointer-only; summaries drift, pointers don't.

## Decision Test (run before writing or editing any doc)

> **"Name the single surface this content belongs on. Is the same content already on another surface?"**

- One surface, no duplication → proceed
- Duplicates content elsewhere → **stop**. Replace with `(README §X)` pointer or delete the duplicate
- Cannot name exactly one surface → consult the table below before writing

## Surface Responsibilities

| Surface | Job | Shape |
|---|---|---|
| **Code comment** | Point-of-use *why* at the line the invariant is enforced | Load-bearing why only: hidden constraint, subtle invariant, bug workaround, deliberate tradeoff. Never restate *what* |
| **Module `README.md`** | Developer narrative for the module | Purpose / Public API / Contracts / Quirks / Tests. Short shape (Purpose + API + Tests) when the module owns no invariant |
| **Module `AGENTS.md`** | LLM map + warning label | 3-8 one-line rules each ending `(README §X)`; Navigation. **≤30 lines hard ceiling** |
| **`docs/architecture.md`** | Cross-module narrative + invariant ownership index | Layout tree, invariant-to-owner table, cross-cutting policies with no single-module owner. Index rows are *links*, not restatements |
| **Root `README.md`** | User-facing project doc | Pitch, install, commands, development pointer, license. Deeper structure delegated to `docs/architecture.md` and `DEVELOPMENT.md` |

Each module with an AGENTS.md has a `CLAUDE.md` containing the single line `@AGENTS.md`.

## Prose Constraints

All five surfaces share one audience: an experienced Go developer reading for task-fit.

- Sentences ≤25 words, averaging 12 to 17.
- Active voice predominant.
- Paragraphs ≤4 sentences.
- Headings predict their content. `§<section name>` cross-refs break the moment a heading goes vague, so heading clarity is load-bearing, not cosmetic.
- Project-specific jargon (e.g. "session-keyed data", "encoded dir") is defined once at root `README.md` §<term> and never re-defined. Go stdlib and language vocabulary stays undefined. The audience is not a novice.

Numbers earn their place. Include a numeric value only when the reader cannot derive it from surrounding text. Decision test: if replacing the number with "several" or deleting it loses no information, delete.

- **Keep** (value carries information): thresholds (`16 MiB`, `64 KiB`), version pins (`huh v2`, `v2.11.4`), indexing conventions (1-based vs 0-based), grammar bounds (`[A-Z0-9_]{1,64}`), manpage section refs (`flock(2)`), and sequence positions in a numbered contract.
- **Strip** (value restates or invents): counts labeling an enumeration the text then gives ("nine-category table", "all five groups"), speculative future cardinalities ("adding a sixth", "the seventh arm").

Human-facing prose (module READMEs, `docs/architecture.md`) additionally follows `anti-ai-slop.md`, loaded when editing those files: no em or en dashes, no slop vocabulary, varied sentence rhythm, concreteness over abstraction. AGENTS.md, code comments, rule files, and superpowers plans/specs under `docs/superpowers/plans/` or `docs/superpowers/specs/` are out of scope.

Do **not** import consumer-readability heuristics:

- **Flesch-Kincaid Grade 8-12** is calibrated for editorial or consumer prose. Go CLI architecture docs live at FK 10-14 and that is correct for the audience. A rewrite pass targeting FK 8-12 forces over-simplification and drops technical precision. Reject the metric, not the prose.
- **Mermaid / diagram mandates** are invented constraints for this project. cc-port has zero diagrams today and no contract needs one; a "prefer diagrams" rule is precisely what the existing *invented constraints are misinformation* Red Flag warns against. Add a diagram only when a table cannot express the relationship.

## Red Flags

| Thought | Reality |
|---|---|
| "Let me rewrite this paragraph to hit Flesch-Kincaid 8" | Developer docs target ~FK 10-14; consumer-grade readability drops precision |
| "A diagram would make this section friendlier" | cc-port docs have no diagrams; add one only when a table cannot express the relationship |
| "Giving the count upfront frames the list that follows" | The list already gives the count. The label is noise, delete it |
| "Future-proof the rule for when a sixth arm appears" | "Sixth" bakes in today's count and breaks on the next add. Write "a new arm" |

## Cross-cutting policies

Cross-cutting invariants with no single-module owner live in `docs/architecture.md` as a *Cross-cutting policies* section. Framing only, with one-line links to per-command handling in each owning module's README. File-history is the current (and only) instance. Before adding a second, confirm no single module enforces the invariant; if one does, the contract belongs there.
