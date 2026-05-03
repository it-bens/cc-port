---
paths:
  - "cmd/**/README.md"
  - "internal/**/README.md"
---

# README.md Rules (cc-port)

**CRITICAL**: A module README is the developer narrative for its module (Purpose / Public API / Contracts / Quirks / Tests). The surface system and prose constraints live in `documentation-architecture.md`. This file covers the one discipline unique to READMEs: the `## Contracts` skeleton.

## Decision Test (run before writing or editing a `## Contracts` section)

> **"Does every invariant in this module have a row under Handled, Refused, or Not covered, and is the residual-risk wording load-bearing, not paraphrased?"**

- Yes → proceed
- A case is missing from all three buckets → add it; the three buckets are the contract
- The skeleton headings were renamed or rewritten → restore verbatim; the skeleton is not yours to refine mid-edit

## Contract Sections

Every `## Contracts` subsection uses the **Handled / Refused / Not covered** skeleton verbatim. The residual-risk lists are the actual contract. Don't rewrite the skeleton during edits. Refinement, if ever, is a separate pass.
