# Skill Changelog

Tracks behavioral changes to committed cc-port skills. Versioning is sequential
(v1, v2, v3...). Semver will land later when external consumers exist; for now
each version is a snapshot of the skill's committed shape after the change.

Newest version first within each skill.

## commit-message-generating

### v1

Placeholder for the initial committed shape. Backfill the entry when the next
change lands.

## writing-docs

### v2

- *Apply the surface shape* (prose branch) loads `references/surface-shapes.md`
  with one new sub-rule: the §Limitations anti-pattern. Constraints actively
  enforced by code belong under §Contracts (Handled / Refused / Not covered),
  not under §Limitations.
- `references/surface-shapes.md` carries the full anti-pattern: signal,
  promotion mapping into the H/R/NC skeleton, the carve-out where §Limitations
  remains correct, and a worked WRONG/CORRECT pair.
- `references/agents-md.md` Decision Test gains two questions per bullet
  beyond the existing pointer-shape check: provenance (the rule must trace to
  a specific cleanup, a recurring bug class, or a load-bearing test invariant)
  and visibility (a violation must produce a visible failure, not just stylistic
  drift). Speculative AGENTS.md rules are now explicitly refused.
- *Final quality gate* expands from two cross-surface checks to three. The
  third check is a code-vs-claim sweep: each contract row or behavior claim
  in the diff must match what the named function actually does today. The
  digraph gains drift-fail back-edges from `gate_pass` to `prose_write` and
  `agents_write`.

### v1

Placeholder for the initial committed shape. Backfill the entry when the next
change lands.

## writing-go-code

### v2

- *Confirm the API call* extends from stdlib-only consultation to also cover
  in-repo domain primitives. When a call would mutate path-shaped strings,
  edit a user-owned config file, or scan untrusted bytes, the project usually
  wraps the stdlib primitive in a domain helper that carries the right
  invariants; the helper is consulted before the stdlib call.
- New reference `references/in-repo-primitives.md` carries the lookup table
  (six call shapes mapped to the corresponding wrapper:
  `rewrite.ReplacePathInBytes` and its JSON-escape variant; `gjson`/`sjson`
  for user-owned JSON; the `claude.MaxHistoryLine`-capped scanner; `os.Root`
  + `io.LimitReader` for archive entries; `claude.ResolveProjectPath` before
  encoding; `fsutil.ResolveExistingAncestor` for symlink resolution) plus a
  decision test and the rationale for why the lookup precedes `go doc`.
- New workflow step *Confirm dependencies and surface scope*. Two
  architectural checks fire after writing the line, before comment
  classification: dependency entry shape (parameters and options preferred
  over reach-for forms like package-level vars, free `os.Getenv`, hidden
  singletons) and caller scope for new exported symbols (every new export
  must have a non-test caller within the same coordinated unit of work,
  defined as a single PR or a chain of dependent plans, specs, and PRs that
  share a feature).
- Digraph gains `surface_decision` and `surface_check` nodes between `write`
  and `comment_decision`. The no-call branch from `call_decision` now flows
  through `write` instead of skipping straight to comments, so the surface
  check fires for non-call edits (struct fields, exported vars).

### v1

Placeholder for the initial committed shape. Backfill the entry when the next
change lands.

## writing-pr-descriptions

### v1

Placeholder for the initial committed shape. Backfill the entry when the next
change lands.

## writing-release-notes

### v1

Placeholder for the initial committed shape. Backfill the entry when the next
change lands.

## writing-tests

### v2

- *Identify the single behavior* gains a third branch on the
  observable-on-exported-API diamond. When the behavior is real but the
  current exported API hides it, the workflow now offers seam introduction
  in production code — `io.Writer` parameters, `WithX` options, exported
  pure helpers, package-level fn-var seams — alongside the existing reframe
  or delete options. The seam must be one production code wants regardless
  of the test; a test-only seam is the same internal-test smell dressed in
  an option.
- `references/behavior.md` adds the four-pattern seam table with production
  shape and test usage per pattern. The Go-specific carve-outs list gains a
  fourth entry: drift-guard tests that assert two registries stay
  index-aligned are valid behavior tests of the registry contract, not
  implementation tests.
- *Source the arrange data* gains a fifth fixture source: `//go:build
  large`-gated production-scale input, paired with a small-cap CI variant
  that exercises the same branches at 1-2 MiB.
- `references/data.md` carries the full pairing pattern: the two-test split
  (CI runs the small-cap variant on every PR; the maintainer runs the
  large-tag variant locally before merging cap-guard changes), the
  `SetMaxXBytes(t, n)` override seam template, and the rule for documenting
  any branch that only manifests at production scale.
- Digraph gains `seamable` diamond and `introduce_seam` box on the
  not-observable branch.

### v1

Placeholder for the initial committed shape. Backfill the entry when the next
change lands.
