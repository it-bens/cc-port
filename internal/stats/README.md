# internal/stats

## Purpose

Computes a project's footprint in `~/.claude`: how many times its path is
referenced across shared files, and how much disk its owned data occupies.
Read-only. It reuses the `claude` enumeration and the `rewrite` count
primitives rather than `move`'s dry-run, which counts a rename's replacements
instead of an inventory.

## Public API

- `ComputeFootprint(ctx context.Context, claudeHome *claude.Home, projectPath string) (*Footprint, error)`:
  the full footprint of one project. Propagates `claude.LocateProject`'s
  not-found and identity-mismatch errors rather than fabricating a zero
  footprint.
- `ComputeAllFootprints(ctx context.Context, claudeHome *claude.Home) ([]ProjectFootprint, error)`:
  every project's disk footprint, ranked by total bytes descending.
- `Footprint`: single-project result. The `ProjectPath` and encoded
  `ProjectDir`, per-surface `References`, per-category `Disk`, the
  `ReferenceTotal` / `DiskFiles` / `DiskBytes` totals, and the structured
  `HistoryEntryCount` and `SessionFileCount`.
- `ProjectFootprint`: one row of the all-projects ranking. A display `Label`
  (resolved real path, or the `EncodedDir` name when `Resolved` is false),
  per-category `Disk`, and the `Files` / `Bytes` totals the ranking sorts on.
- `DiskUsage`: a category's `Files` count and `Bytes` size.
- `ReferenceCount`: a surface's occurrence `Count`.

All types are JSON-marshalable; the cmd layer emits them under the root
`--json` flag.

## Contracts

### Metric scoping

The two modes report different metrics by design. Single-project reports both
references and disk. All-projects reports disk only, ranked.

References require the project's real path; an arbitrary encoded directory
recovers one only through a session witness, and a per-project scan of every
shared file across every project would be costly. Disk metrics need no real
path. So the all-projects mode omits references rather than report a cheaper,
inconsistent approximation. See
[`internal/claude/README.md`](../claude/README.md) §All-projects enumeration
for the label resolution and the witness-less fallback.

### Per-surface count variant

Reference counts route through the `rewrite` count primitives, never
`strings.Count`, so the boundary contract holds in one place (see
[`internal/rewrite/README.md`](../rewrite/README.md) §Boundary rules). Each
surface uses the variant that matches what an apply would actually rewrite
there:

| Surface | Variant | Reason |
|---|---|---|
| `history`, `sessions`, `config` | JSON-escape | Apply rewrites these through the typed JSON helpers, which can emit `\/`. |
| `transcripts`, `memory` | raw, plus a raw encoded-dir pass | Apply rewrites these with the plain replacer, and each embeds the encoded `~/.claude/projects/<encoded>` form. |
| user-wide and session-keyed flat files | raw | Apply rewrites these with the plain replacer. |

The encoded-dir pass keys on the absolute project directory
(`claudeHome.ProjectDir(path)`), mirroring `move`'s two-pass rewrite of
transcripts and memory. `history` references count occurrences across every
well-formed line, capped per line by `claude.MaxHistoryLine`; malformed lines
are skipped because an apply preserves them verbatim.

#### Handled

- A path bounded by a sibling prefix (`/p/myproject-extras` while counting
  `/p/myproject`) does not count, because the boundary rule rejects it.
- `HistoryEntryCount` (lines whose `project` field equals the path) and the
  `history` reference count (path occurrences across all well-formed lines)
  are reported as separate, deliberately different numbers.

#### Refused

- A reference count for `file-history`. Snapshot bytes are opaque and never
  scanned (see [`docs/architecture.md`](../../docs/architecture.md)
  §File-history policy (cross-cutting)).

#### Not covered

- A move applied without `--rewrite-transcripts` leaves transcripts untouched,
  so the unconditional transcript reference count reflects what such a move
  *would* touch if the flag were set, not a default move.

### Disk-footprint categories

Disk usage is keyed by `manifest.AllCategories` name and ordered by that
slice, never a hard-coded list. `sessions` sums the project-dir transcript
bodies (top-level `*.jsonl` plus every file under each non-`memory`/`sessions`
subdirectory) and the project's `sessions/*.json`; `memory`, `file-history`,
and the session-keyed categories (`todos`, `usage-data`, `plugins-data`,
`tasks`) size their owned files. `history` and `config` are
shared globals with no per-project disk footprint and stay at zero, present in
the result so the DTO always carries the full category set in order.

## Tests

Unit tests in `stats_test.go` cover the boundary-aware reference exclusion of
prefix siblings, the structured-vs-reference count distinction, the
per-category disk breakdown, the manifest-derived ordering, the
encoded-storage-dir pass, the not-found propagation, and the all-projects
ranking with witness-resolved labels. The cmd-layer rendering and `--json`
tests live in `cmd/cc-port`.
