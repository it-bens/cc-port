# Surface Shapes (reference)

Loaded from `writing-docs` SKILL.md on the human-prose branch of the *Apply the surface shape* step.

Three human-prose surfaces in scope here. Each has a fixed shape that is not yours to refine mid-edit. AGENTS.md is covered by `references/agents-md.md`; code comments live with `writing-go-code`.

## Module README

**Sections:** Purpose / Public API / Contracts / Quirks / Tests.

**Short shape:** Purpose / Public API / Tests is the entire README when the module owns no invariant. Don't pad with a Contracts section that says "none".

### §Contracts skeleton

Every `## Contracts` subsection uses the **Handled / Refused / Not covered** skeleton verbatim. The residual-risk lists are the actual contract; the skeleton is the contract's shape, not yours to rewrite. Refinement, if ever, is a separate pass.

```markdown
## Contracts

### {Invariant name}

**Handled.** {What the module does to satisfy the invariant. One sentence per case the implementation covers.}

- {Case 1.}
- {Case 2.}

**Refused.** {What the module rejects rather than handle. The refusal is part of the contract.}

- {Refused case 1, with the error or panic the caller sees.}

**Not covered.** {What the module does not address. The residual risk the caller carries.}

- {Uncovered case 1, with the consequence if the caller hits it.}
```

The Handled / Refused / Not covered triple is the residual-risk decomposition. Every invariant the module owns has rows in all three buckets, even if a bucket has only "none" — explicit "none" is a contract, an absent bucket is a gap.

### Decision Test

Before writing or editing a §Contracts section: does every invariant in this module have a row under Handled, Refused, or Not covered, and is the residual-risk wording load-bearing rather than paraphrased?

- A case missing from all three buckets → add it; the three buckets are the contract.
- The skeleton headings were renamed or rewritten → restore verbatim.

## docs/architecture.md

**Job:** cross-module narrative + invariant ownership index.

**Shape:** layout tree (directory tour), invariant-to-owner table (one row per invariant, one link per owner), cross-cutting policies with no single-module owner.

Index rows are *links* to the owning module's README §<section>, not restatements of the contract. The index says "X is enforced by module Y, see §Z" — the actual contract lives at §Z.

### Cross-cutting policies

Cross-cutting invariants with no single-module owner live in `docs/architecture.md` as a *Cross-cutting policies* section. Framing only, with one-line links to per-command handling in each owning module's README. File-history is the current (and only) instance of a cross-cutting policy. Before adding a second, confirm no single module enforces the invariant; if one does, the contract belongs there and the architecture index gets a link, not a duplicate.

## Root README

**Sections:** pitch, install, commands, development pointer, license. User-facing.

**Delegate, don't expand.** Deeper structure delegates to `docs/architecture.md` and `DEVELOPMENT.md`. The root README does not re-explain module contracts or cross-cutting policies; it points at architecture.md and stops.

