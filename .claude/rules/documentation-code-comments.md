---
paths:
  - "**/*.go"
---

# Code Comment Rules (cc-port)

**CRITICAL**: Code comments carry point-of-use *why*: the invariant a reader needs to know *at this line*. Comments that restate *what* the code does, paraphrase the identifier, or narrate stdlib are deleted. Exported godoc is kept and compressed, never removed.

## Decision Test (run before keeping, editing, or deleting any comment)

> **"Does this comment name a failure mode, hidden constraint, or deliberate tradeoff that a reader cannot infer from the code?"**

- Yes → **keep verbatim**; rewrite new why-comments toward this shape
- No → classify via the table below and apply the listed action

## Classification

| Bucket | Action |
|---|---|
| **Redundant with README** (70%+ wording overlap with a README contract) | Remove or compress to one line |
| **Explains-what** (paraphrases the identifier or the code below) | Remove |
| **Tutorial / novice-facing** (narrates stdlib or flow) | Remove or compress sharply |
| **Over-specified why** (five lines where one would do) | Tighten |
| **Load-bearing why** (names the failure mode the code guards against) | **Keep verbatim**; rewrite new why-comments toward this shape |
| **Exported godoc** (required by `revive: exported`) | Keep; compress pure paraphrase to one identifier-prefixed line, never remove |

Exemplars: `internal/export/export.go:applyPlaceholders` and `internal/importer/resolve.go:applyResolutions`. Both *name the failure mode the code guards against*.

No backlinks (`// see README §X`) at trim sites. Directory colocation + the AGENTS.md pointer map already bridge. Exception: one `see internal/<other>/README.md` when the referenced material lives in a *different* module.

## Banned Patterns

```
WRONG:   // CopyFile copies src to dst. It opens src for reading,
         // creates dst with the same mode, and streams the bytes.
CORRECT: (delete; identifier + signature already say this)

WRONG:   // see README §Atomic staging
         stageFile(...)
CORRECT: stageFile(...)   // README is adjacent; the pointer is noise

WRONG:   see internal/importer/README.md:147
CORRECT: see internal/importer/README.md §Atomic staging
```

## Red Flags

| Thought | Reality |
|---|---|
| "Let me add `// see README §X` above every function whose why moved" | Scaffolding leaking into code. The directory listing already bridges |
| "This comment restates the function name but removing it feels risky" | If removal subtracts no information from an experienced reader, remove it |
