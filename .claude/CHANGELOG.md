# .claude Changelog

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/). Covers everything committed under `.claude/`: skills, extensions, and other configuration. Versioning is sequential per component (v1, v2, v3...).

## commit-message-generating

### v2

#### Removed
- Entire skill. Replaced by the `commit-message-writer@itb-ai-tools` plugin's `writing-commit-messages` skill plus the project overlay `.claude/hook-contexts/writing-commit-messages.md`.

### v1

#### Added
- 13-step workflow covering three modes (staged, squash, rewrite), differing only in the git range passed to the gather script.
- `scripts/gather.sh` writes all git output to a single `/tmp` file and prints a section TOC; subsequent steps issue targeted Read and Grep calls against that file rather than streaming diffs through context.
- Three references: `scope-detection.md`, `type-detection.md`, `writing-rules-anti-ai-slop.md`.
- Clipboard offer (`pbcopy` / `xclip`) and tmp-file cleanup as the final action.

## extensions/software-writer

### v1

#### Added
- One extension file per plugin skill (`writing-code.md`, `writing-tests.md`, `writing-docs.md`) carrying cc-port's named-value assignments and workflow-position sections. Delivered by the `software-writer@itb-ai-tools` plugin on Claude Code and via the root `AGENTS.override.md` on Codex.

## hook-contexts/brainstorming

### v2

#### Changed
- Sections renamed from step-numbered anchors tied to the parent skill's numbering (`## Step 7: ...`) to self-contained, phase-named headings, so the overlay survives parent-skill renumbering.

## hook-contexts/reviewing-plans

### v2

#### Changed
- Sections renamed from step-numbered anchors to phase-named, self-contained instructions; the spec example path and the skill-ladder table are folded into prose.
- Writing-skill references repointed from the removed project skills to the `software-writer` plugin skills.

## hook-contexts/writing-commit-messages

### v1

#### Added
- Overlay for the `commit-message-writer@itb-ai-tools` plugin's `writing-commit-messages` skill: a Pre-Step-8 type rule (`.claude/**` is code, never `docs`) and a Pre-Step-9 scope vocabulary derived from commit history. Delivered through two `jq` hook entries in `.claude/settings.json`.

## writing-docs

### v4

#### Removed
- Entire skill. Replaced by the `software-writer@itb-ai-tools` plugin's `writing-docs` skill plus the project extension `.claude/extensions/software-writer/writing-docs.md`.

### v3

#### Changed
- All four references drop the "Loaded from SKILL.md when..." intro; the SKILL.md routing already gates the load.
- `anti-ai-slop.md` drops the meta + scope sentences after the fingerprint paragraph and the numbers paragraph from §Concreteness over abstraction. Numbers guidance stays owned by `writing-style.md`. Em-dash density numbers stay — they are grounded evidence for the hard ban, not orienting prose.
- `writing-style.md` drops the audience line that the SKILL.md already states.
- `agents-md.md` §Existence check leads with the cc-port examples; the rule restatement and "ceremonial = noise" sentence move out (SKILL.md owns both). The "next AGENTS.md carries less weight" rationale stays.

### v2

#### Added
- *Apply the surface shape* (prose branch) loads `references/surface-shapes.md` with one new sub-rule: the §Limitations anti-pattern. Constraints actively enforced by code belong under §Contracts (Handled / Refused / Not covered), not under §Limitations.
- `references/surface-shapes.md` carries the full anti-pattern: signal, promotion mapping into the H/R/NC skeleton, the carve-out where §Limitations remains correct, and a worked WRONG/CORRECT pair.
- `references/agents-md.md` Decision Test gains two questions per bullet beyond the existing pointer-shape check: provenance (the rule must trace to a specific cleanup, a recurring bug class, or a load-bearing test invariant) and visibility (a violation must produce a visible failure, not just stylistic drift). Speculative AGENTS.md rules are now explicitly refused.

#### Changed
- *Final quality gate* expands from two cross-surface checks to three. The third check is a code-vs-claim sweep: each contract row or behavior claim in the diff must match what the named function actually does today. The digraph gains drift-fail back-edges from `gate_pass` to `prose_write` and `agents_write`.

### v1

#### Added
- Workflow branches by surface (root `README.md` / module `README.md` / `docs/architecture.md` vs. `AGENTS.md`), applies the matching surface skeleton, writes the content, runs an anti-slop pass for human-prose surfaces, then closes with a two-check final quality gate (single-surface principle, cross-ref integrity).
- The AGENTS.md branch short-circuits when the module owns no hard cross-cutting constraint.
- Four references: `agents-md.md`, `anti-ai-slop.md`, `surface-shapes.md`, `writing-style.md`. Each reference opens with a "Loaded from SKILL.md when..." intro.

## writing-go-code

### v4

#### Removed
- Entire skill. Replaced by the `software-writer@itb-ai-tools` plugin's `writing-code` skill plus the project extension `.claude/extensions/software-writer/writing-code.md`.

### v3

#### Changed
- All three references drop the "Loaded from SKILL.md when..." intro. `in-repo-primitives.md` keeps the wrapper-invariants framing sentence.

#### Removed
- `comments.md` §"When the comment seems redundant but feels load-bearing"; the decision procedure restated the §Classification table's load-bearing-why bucket and the godoc paragraph restated its exported-godoc row.
- `in-repo-primitives.md` §"Why this lookup precedes `go doc`"; the SKILL.md already carries the framing.

### v2

#### Added
- New reference `references/in-repo-primitives.md` carries the lookup table (six call shapes mapped to the corresponding wrapper: `rewrite.ReplacePathInBytes` and its JSON-escape variant; `gjson`/`sjson` for user-owned JSON; the `claude.MaxHistoryLine`-capped scanner; `os.Root` + `io.LimitReader` for archive entries; `claude.ResolveProjectPath` before encoding; `fsutil.ResolveExistingAncestor` for symlink resolution) plus a decision test and the rationale for why the lookup precedes `go doc`.
- New workflow step *Confirm dependencies and surface scope*. Two architectural checks fire after writing the line, before comment classification: dependency entry shape (parameters and options preferred over reach-for forms like package-level vars, free `os.Getenv`, hidden singletons) and caller scope for new exported symbols (every new export must have a non-test caller within the same coordinated unit of work, defined as a single PR or a chain of dependent plans, specs, and PRs that share a feature).

#### Changed
- *Confirm the API call* extends from stdlib-only consultation to also cover in-repo domain primitives. When a call would mutate path-shaped strings, edit a user-owned config file, or scan untrusted bytes, the project usually wraps the stdlib primitive in a domain helper that carries the right invariants; the helper is consulted before the stdlib call.
- Digraph gains `surface_decision` and `surface_check` nodes between `write` and `comment_decision`. The no-call branch from `call_decision` now flows through `write` instead of skipping straight to comments, so the surface check fires for non-call edits (struct fields, exported vars).

### v1

#### Added
- Two-step workflow: *Confirm the API call* (consult `go doc <pkg>.<Symbol>` before any non-builtin Go library call, with mechanical-idiom and already-passing-test exceptions) and *Classify each comment* (six-bucket table — redundant-with-README, explains-what, tutorial, over-specified why, load-bearing why, exported godoc — each mapped to a delete / compress / keep action).
- Two references: `comments.md`, `go-doc.md`. Each reference opens with a "Loaded from SKILL.md when..." intro.

## writing-pr-descriptions

### v1

#### Added
- Workflow gathers the PR via gh-tooling (`pr_view`, `pr_files`, `pr_diff`, `pr_commits`), classifies the changed files into cc-port modules (CLI, command modules, primitives, tests, CI, build, docs, config), detects the conventional-commit type with a confidence level, flags dependency-update and breaking-change special cases, filters auxiliary tests/docs, then routes to one of five templates: Standard, CI-only, Build/Release, Documentation-only, Dependency.
- Anti-slop pass and clipboard offer close the run.
- Two references: `type-detection.md`, `writing-rules-anti-ai-slop.md`.
- Read-only — never mutates the PR.

## writing-release-notes

### v1

#### Added
- Workflow resolves a tag range (with first-release fallback to `<initial-commit>..HEAD` and a confirm prompt when the target tag does not exist locally), lists merged PRs from `git log <range>`, fetches each PR's title / body / labels via gh-tooling `pr_view` (`commit_pulls` covers direct pushes), categorises by conventional-commit prefix and changed paths, filters version-bump and dep-bump noise, flags breaking changes, and drafts notes against a fixed skeleton (Summary / Changes / Breaking Changes / Upgrade Notes).
- Anti-slop pass explicitly rewrites `- **Title**: description.` bullets into plain prose.
- One reference: `writing-rules-anti-ai-slop.md`.
- Read-only — never mutates the release.

## writing-tests

### v4

#### Removed
- Entire skill. Replaced by the `software-writer@itb-ai-tools` plugin's `writing-tests` skill plus the project extension `.claude/extensions/software-writer/writing-tests.md`.

### v3

#### Changed
- All four references drop the "Loaded from SKILL.md when..." intro.
- `behavior.md` merges §"When accessor / constructor tests ARE valid" inline into §"Do NOT test" as carve-out parentheticals; the standalone inverse list is removed.
- `independence.md` drops the meta-instruction telling the LLM how to read the file. The baseline snapshot stays.

### v2

#### Added
- *Identify the single behavior* gains a third branch on the observable-on-exported-API diamond. When the behavior is real but the current exported API hides it, the workflow now offers seam introduction in production code — `io.Writer` parameters, `WithX` options, exported pure helpers, package-level fn-var seams — alongside the existing reframe or delete options. The seam must be one production code wants regardless of the test; a test-only seam is the same internal-test smell dressed in an option.
- `references/behavior.md` adds the four-pattern seam table with production shape and test usage per pattern. The Go-specific carve-outs list gains a fourth entry: drift-guard tests that assert two registries stay index-aligned are valid behavior tests of the registry contract, not implementation tests.
- *Source the arrange data* gains a fifth fixture source: `//go:build large`-gated production-scale input, paired with a small-cap CI variant that exercises the same branches at 1-2 MiB.
- `references/data.md` carries the full pairing pattern: the two-test split (CI runs the small-cap variant on every PR; the maintainer runs the large-tag variant locally before merging cap-guard changes), the `SetMaxXBytes(t, n)` override seam template, and the rule for documenting any branch that only manifests at production scale.

#### Changed
- Digraph gains `seamable` diamond and `introduce_seam` box on the not-observable branch.

### v1

#### Added
- Five-step workflow: *Identify the single behavior* (do-test / don't-test classification, exported-API observability gate with reframe-or-delete branch), *Source the arrange data* (four sources: `testutil.SetupFixture`, `testdata/<name>` sibling or `//go:embed`, inline literal, `t.TempDir`), *Structure the body* (AAA for 5+ statements, table-driven for shorter, `wantErr` carve-out for conditional assertions), *Guard independence* (four leak vectors: package-level vars, subtest closure capture, unrestored globals, wall-clock / randomness in asserted values), *Final quality gate* (two checks: redundancy and guard-clause isolation).
- Four references: `behavior.md`, `data.md`, `independence.md`, `shape.md`. Each reference opens with a "Loaded from SKILL.md when..." intro.
