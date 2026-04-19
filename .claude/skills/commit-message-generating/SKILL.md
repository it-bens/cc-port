---
name: commit-message-generating
description: Generate conventional commit messages for the Shopware AI Coding Tools marketplace. Determines type, infers scope from plugin directory structure, and detects breaking changes. Use when generating commit messages in this repository.
allowed-tools: Read, Grep, Bash, AskUserQuestion
model: haiku
---

# Commit Message Generating

Generate conventional commit messages for the Shopware AI Coding Tools marketplace repository.

## Requirements

- Working directory is this repository
- **Staged mode**: staged or unstaged changes for a single commit message
- **Squash mode**: a branch with commits diverged from `main`
- **Rewrite mode**: a commit hash provided as argument to rewrite its message

## Mode Detection

- **Rewrite mode**: argument is a commit hash (full or abbreviated SHA)
- **Squash mode**: user mentions "squash", "branch", "PR", or asks for a commit message summarizing a branch
- **Staged mode** (default): all other cases

## Project Rules

**Types** (all allowed): feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert

**Scopes** — two categories:
1. **Plugin scopes**: directory names under `plugins/` (emitted by the gather script as the `plugins` section)
2. **Infrastructure scopes**: `hooks`, `marketplace`, `ci`, `github`

**Scope omission** — omit scope when:
- Type is `docs` with project-wide files (README.md, CONTRIBUTING.md, AGENTS.md)
- Type is `ci` with only CI config changes
- Root-level config files (.gitignore, LICENSE, pyproject.toml)
- Cross-cutting changes spanning 3+ unrelated plugins

**Subject**: imperative mood, lowercase, no period, max 72 chars.

**Body**: optional for most commits. Required for breaking changes (must include migration instructions).

**Attribution footer**: always enabled.

**No ticket format required.**

---

## Workflow

The workflow is the same for all three modes. Only the git range passed to the gather script differs.

### Step 1: Gather

Determine the git range for the mode:

| Mode    | Range                  |
|---------|------------------------|
| staged  | `--cached`             |
| squash  | `<base>..HEAD` (default base: `main`) |
| rewrite | `<hash>^..<hash>`      |

Run the gather script:

```
bash .claude/skills/commit-message-generating/scripts/gather.sh <range>
```

The script writes all git output to a single file in `/tmp` and prints a table of contents on stdout. Example output:

```
TMPFILE=/tmp/cc-port-commit.aB3kQ9

SECTION plugins      2-13
SECTION status       15-40
SECTION shortstat    42-42
SECTION numstat      44-68
SECTION log          70-122
SECTION diff         124-1840

DIFF_FILE AGENTS.md                                   124-139
DIFF_FILE cmd/cc-port/export.go                       140-299
DIFF_FILE internal/manifest/categories.go             300-431
...
```

Record `TMPFILE` and the section ranges. A section whose range is inverted (`start > end`) is empty — skip reading it. The `log` section is omitted entirely for staged mode.

Exit codes from the script:
- `0`: success, TOC printed.
- `1`: no changes in range. For staged mode, retry with an empty range to check unstaged changes.
- `2`: invalid range. Report the stderr message to the user and stop.

### Step 2: Orient

Read the plugins, status, and shortstat sections together with one `Read` call (offset = plugins.start, limit covers through shortstat.end). This loads the scope vocabulary, the file change list with rename detection, and the one-line stat summary.

### Step 3: Prioritize

Read the `numstat` section. Rank files by churn and status to identify which files need semantic inspection.

### Step 4: Prior Context (squash and rewrite only)

If the TOC contains a `log` section, read it.

- **Squash mode**: per-commit subjects and bodies seed the draft body. Draft the summary from the subjects first; later steps verify and fill gaps.
- **Rewrite mode**: the existing commit message is the current claim. Use it as a starting reference, but base type and scope purely on the diff.

### Step 5: Inspect Content

For each file the draft will make a specific claim about, read the corresponding `DIFF_FILE` range from the TOC.

This step is authoritative. Every sentence in the subject and body must be traceable to a hunk read here. Content is the source of truth — do not infer behaviour from file paths or change counts alone.

### Step 6: Query Signals

Use `Grep` with `path=$TMPFILE` for cross-cutting checks that don't belong to one file:

- Removed exported symbols: `^-func `, `^-type `, `^-class `, `^-export `
- Breaking change markers: `BREAKING`, `breaking change`
- Specific identifiers mentioned in the draft

Grep keeps the diff authoritative while targeting only the lines that match — the full diff never needs to load into context.

### Step 7: Determine Type

See [references/type-detection.md](references/type-detection.md) for the decision tree.

- HIGH/MEDIUM confidence: use type directly.
- LOW confidence: use `AskUserQuestion` with options from the analysis.
- Detect breaking change indicators.

### Step 8: Infer Scope

See [references/scope-detection.md](references/scope-detection.md) for the rules. Apply them to the file list from the `status` section.

- HIGH/MEDIUM confidence: use scope directly.
- LOW confidence: use `AskUserQuestion`.

### Step 9: Craft Subject and Message

**Subject rules**: imperative mood, lowercase, no period, max 72 chars, specific description.

**Body rules**: do not hard-wrap body lines at 72 characters. Write each paragraph as a single continuous line. The 72-char limit applies only to the subject.

**Squash body**: describe the branch's purpose. Group by logical concern, not by commit order.

**Writing quality**: read [references/writing-rules-anti-ai-slop.md](references/writing-rules-anti-ai-slop.md) and apply all rules to the subject and body.

**Message format**:

```
type(scope): subject

body (if breaking change or complex multi-file change)

BREAKING CHANGE: description (if breaking)

Co-Authored-By: Claude <model-name> <noreply@anthropic.com>
```

Use your actual model name (e.g. "Opus 4.7 (1M context)", "Sonnet 4.6") for `<model-name>`.

Do NOT include PR references like `(#N)` — GitHub adds these during merge.

### Step 10: Anti-Slop Validation

Re-read [references/writing-rules-anti-ai-slop.md](references/writing-rules-anti-ai-slop.md), then check the draft literally (not from memory):

1. Check each body paragraph for hard-wrapping. Join multi-line paragraphs into a single continuous line. The 72-char limit applies only to the subject.
2. Search subject and body for em dash (—) and en dash (–). Remove every instance. Check this as a literal character search, not a mental scan.
3. Re-read each word against the banned vocabulary list. Replace matches with the plain alternative or delete.
4. Check for banned sentence patterns, colon/semicolon overuse, hedging filler.
5. If any violations found, rewrite the affected text and re-check.

### Step 11: Present

Quick self-check: type matches changes, scope matches files, subject is accurate.

**Output**: brief analysis (type reasoning, scope reasoning, breaking changes), then the commit message in a code block.

### Step 12: Clipboard Offer

Ask the user whether to copy the message to the clipboard. If they accept, copy the message (without the surrounding code block markers) using `pbcopy` on macOS or `xclip -selection clipboard` on Linux.

### Step 13: Cleanup

Delete the tmp file as the final action, regardless of outcome:

```
rm -f "$TMPFILE"
```

---

## Error Handling

The gather script surfaces most errors via exit codes and stderr. The skill handles:

- **Staged mode, exit code 1**: nothing staged. Retry with an empty range to use unstaged changes. If that also returns exit 1, inform the user there is nothing to commit.
- **Squash mode, exit code 1**: branch has no commits ahead of base. Inform the user.
- **Rewrite mode, exit code 2**: hash does not resolve to a commit. Inform the user.
