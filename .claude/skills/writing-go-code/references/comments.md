# Code Comments (reference)

Loaded from `writing-go-code` SKILL.md when the workflow needs the deep technical detail behind the *Classify each comment* step.

## Classification table

| Bucket | Action |
|---|---|
| **Redundant with README** (70%+ wording overlap with a README contract) | Remove or compress to one line |
| **Explains-what** (paraphrases the identifier or the code below) | Remove |
| **Tutorial / novice-facing** (narrates stdlib or flow) | Remove or compress sharply |
| **Over-specified why** (five lines where one would do) | Tighten |
| **Load-bearing why** (names the failure mode the code guards against) | **Keep verbatim**; rewrite new why-comments toward this shape |
| **Exported godoc** (required by `revive: exported`) | Keep; compress pure paraphrase to one identifier-prefixed line, never remove |

### Load-bearing why: worked example

A load-bearing why-comment names the failure mode, gives a concrete instance, and shows what the code does to prevent it. Compare against this shape when writing or keeping a why-comment:

```go
// prevent a shorter placeholder from consuming a legitimate prefix of a
// longer one that ends at a real `/` boundary. For example, substituting
// `/Users/x` → `{{HOME}}` before `/Users/x/project` → `{{PROJECT_PATH}}`
// would leave `{{HOME}}/project` where `{{PROJECT_PATH}}` was intended.
// Sorting longest-first resolves this.
func applyPlaceholders(data []byte, placeholders []manifest.Placeholder) []byte {
    // ... sort by descending Original length, then substitute in order.
}
```

The comment names the failure (shorter prefix consuming a longer key), gives the substitution that triggers it, states the outcome, and identifies the fix. None of those facts is recoverable from the code alone — sorting longest-first looks like a stylistic choice without the comment.

A second shape is the *negative* invariant comment: justify why an obvious-looking guard is deliberately omitted. Example: a placeholder substituter that does plain `bytes.ReplaceAll` and skips boundary checks should explain that `{{UPPER_SNAKE}}` tokens are self-delimiting (the `}}` suffix terminates, no token is a prefix of another), so a boundary check would refuse legitimate substitutions like `{{PROJECT_PATH}}.` in prose. Without that comment, the next editor will "fix" the missing guard and break correctness.

No backlinks (`// see README §X`) at trim sites. Directory colocation plus the AGENTS.md pointer map already bridge. Exception: one `see internal/<other>/README.md` when the referenced material lives in a *different* module.

## Banned patterns

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

## When the comment seems redundant but feels load-bearing

If removal subtracts no information from an experienced Go reader, remove it. The instinct that "feels risky" is usually a paraphrase of the function name, not a hidden invariant. Test by asking: what failure mode does this comment warn the next editor about? If the answer is "none, it explains what the code does", delete.

For exported symbols, godoc is non-negotiable (revive enforces it). Compress pure paraphrase to one identifier-prefixed line — never remove the doc itself.
