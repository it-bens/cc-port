# internal/stats

## Purpose

Generic project-footprint orchestration across every selected tool: for one
project, how many times its path is referenced across shared files and how
much disk its owned data occupies; across all projects, a disk-usage
ranking. Read-only and lock-free, driven entirely by each target's
`tool.Auditor` methods. This package has no tool-specific knowledge; every
surface's actual counting or sizing logic lives in the owning adapter (for
example `internal/tool/claude`).

## Public API

- `ComputeFootprint(ctx context.Context, targets []tool.Target, projectPath string) (*Footprint, error)`:
  the full footprint of one project across every target. A target reporting
  `tool.ErrProjectAbsent` from either `ReferenceSurfaces` or `DiskCategories`
  contributes a zero `ToolFootprint` (`Absent: true`) rather than failing the
  whole call.
- `ComputeAllFootprints(ctx context.Context, targets []tool.Target) ([]ProjectFootprint, error)`:
  every target's known projects (via `Workspace.EnumerateProjects`), flattened
  into one list and ranked by total bytes descending across every tool
  combined (ties broken by label).
- `Footprint`: `ProjectPath`, `ByTool []ToolFootprint` (one per target, in
  registration order).
- `ToolFootprint`: `Tool`, `Absent`, `References []tool.CountSurface`,
  `ReferenceTotal`, `Disk []tool.SizeCategory`, `DiskFiles`, `DiskBytes`.
- `ProjectFootprint`: `Tool` plus an embedded `tool.ProjectInfo` (`Label`,
  `Resolved`, `Disk`, `Files`, `Bytes`), one row of the all-projects ranking.

All types are JSON-marshalable; the cmd layer emits them under the root
`--json` flag.

## Contracts

### Metric scoping

The two modes report different metrics by design. Single-project reports
both references and disk. All-projects reports disk only, ranked.

References require the project's real path; an arbitrary tool-reported
project label recovers one only when that tool can resolve it (for Claude, a
session witness; see `internal/tool/claude/README.md` §All-projects
enumeration), and a per-project scan of every shared file across every
project would be costly. Disk metrics need no real path. So the all-projects
mode omits references rather than report a cheaper, inconsistent
approximation.

#### Handled

- A target reporting `tool.ErrProjectAbsent` contributes a zero,
  `Absent: true` footprint rather than failing the whole sweep; every other
  target still computes normally.
- All-projects enumeration flattens every target's `EnumerateProjects` result
  into one combined ranking rather than reporting one ranking per tool.

#### Refused

- A reference count in all-projects mode. Single-project mode is the only
  place references are reported.

#### Not covered

- What counts as a "reference" or a "disk category" for any given tool. That
  is each adapter's `Auditor` implementation; see
  [`internal/tool/claude/README.md`](../tool/claude/README.md) §Reference and
  disk accounting (stats) for the Claude instance.

## Tests

Unit tests in `stats_test.go` and `boundary_test.go`. Coverage: one
`ToolFootprint` entry per target, the `tool.ErrProjectAbsent` not-found path
reported as `Absent` rather than an error, the disk-footprint and
reference-total aggregation across a target's `SizeCategory`/`CountSurface`
rows, and the all-projects ranking sorted by bytes descending with a
deterministic label tie-break.

Per-adapter reference-counting and disk-sizing behavior (which surfaces
exist, which count variant each uses, boundary-aware exclusion of prefix
siblings) is tested in each adapter package; see
`internal/tool/claude/README.md` §Tests for the Claude instance.
