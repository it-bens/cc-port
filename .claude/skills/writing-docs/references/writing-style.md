# Writing Style (reference)

Loaded from `writing-docs` SKILL.md on the human-prose branch of the *Write the content* step.

Audience for all three human-prose surfaces: an experienced Go developer reading for task-fit. The constraints below calibrate prose to that audience.

## Sentence and paragraph constraints

- Sentences ≤25 words, averaging 12 to 17.
- Active voice predominant. Passive only when the agent is genuinely uninteresting (`the file is staged before the rename`).
- Paragraphs ≤4 sentences.

## Headings predict their content

`§<section name>` cross-refs are the navigation backbone of cc-port docs. They break the moment a heading goes vague, so heading clarity is load-bearing rather than cosmetic.

| Heading | Verdict |
|---|---|
| `## Atomic staging` | Predicts content; safe to cite as `§Atomic staging` |
| `## Concurrency guard` | Predicts content; safe to cite |
| `## Implementation notes` | Vague; `§Implementation notes` will rot when content drifts |
| `## Details` | Vague; rename to what the section actually covers |

When you rename a heading, search for `§<old name>` across the codebase and update every cross-ref in the same edit.

## Jargon discipline

Project-specific jargon is defined once at `docs/architecture.md` §<term> and never re-defined. Examples currently in use: `§Session-UUID-keyed user-wide data`, `§File-history policy`. A doc surface that re-defines one of these is creating a second canonical definition that will drift from the first.

Go stdlib and language vocabulary stays undefined. The audience knows what `bufio.Scanner`, `os.Root`, `io.LimitReader`, `flock(2)`, `t.TempDir`, and `errors.Is` are. Defining them adds noise and signals the wrong audience.

## Numbers earn their place

Include a numeric value only when the reader cannot derive it from surrounding text. Decision test: if replacing the number with "several" or deleting it entirely loses no information, delete.

**Keep** (value carries information):

- Thresholds: `16 MiB`, `64 KiB`, `4096 bytes`
- Version pins: `charm.land/huh/v2 v2.0.3`
- Indexing conventions: 1-based vs 0-based
- Grammar bounds: `[A-Z0-9_]{1,64}`
- Manpage section refs: `flock(2)`
- Sequence positions in a numbered contract

**Strip** (value restates or invents):

- Counts labeling an enumeration the text then gives ("nine-category table" when the table follows; "all five groups" when the five are listed)
- Speculative future cardinalities ("adding a sixth", "the seventh arm") — write "a new arm" instead

## Reject consumer-readability heuristics

Two heuristics from consumer/editorial writing routinely surface as suggested rewrites. Both are wrong for this audience.

**Flesch-Kincaid Grade 8-12** is calibrated for editorial or consumer prose. Go CLI architecture docs live at FK 10-14 and that is correct for the audience. A rewrite pass targeting FK 8-12 forces over-simplification and drops technical precision. Reject the metric, not the prose.

**Mermaid / diagram mandates** are invented constraints for this project. cc-port has zero diagrams today and no contract needs one; a "prefer diagrams" rule is precisely the *invented constraints are misinformation* failure mode. Add a diagram only when a table cannot express the relationship.

## Worked rejections

| Suggested rewrite | Rejection |
|---|---|
| "Let me rewrite this paragraph to hit Flesch-Kincaid 8" | Developer docs target FK 10-14; consumer-grade readability drops precision. |
| "A diagram would make this section friendlier" | cc-port docs have no diagrams; add one only when a table cannot express the relationship. |
| "Giving the count upfront frames the list that follows" | The list already gives the count. The label is noise; delete it. |
| "Future-proof the rule for when a sixth arm appears" | "Sixth" bakes in today's count and breaks on the next add. Write "a new arm". |
