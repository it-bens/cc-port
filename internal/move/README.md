# internal/move

## Purpose

Generic move orchestration across every selected tool: preflight each
target's move surfaces, acquire its lock, apply its surfaces in order, and
report a per-tool result. This package has no tool-specific knowledge; every
surface's actual rewrite (which files, which database columns, what counts
as "the project") lives in the owning adapter's `MoveSurfaces` (for example
`internal/tool/claude`).

## Public API

- `DryRun(ctx context.Context, targets []tool.Target, options Options) (*Plan, error)`:
  computes the plan without writing any files or taking a lock. For each
  target, calls `Workspace.MoveSurfaces` then `Surface.Plan` on each returned
  surface, plus `Workspace.ResidualWarnings`.
- `Apply(ctx context.Context, targets []tool.Target, options Options) (*ApplyResult, error)`:
  executes the move. Every selected target is preflighted in registry order
  (`MoveSurfaces`, then witness-first `lock.Acquire`) before any tool
  applies; the acquired locks are held through the full apply and released
  in reverse order via a deferred cleanup. See
  `docs/architecture.md` §Crash and idempotence contract for the per-tool
  apply bracket, in-process failure, re-run convergence, and cross-tool
  rollback guarantees this call implements. `ApplyResult` carries a per-tool
  success/failure record; `(*ApplyResult).Failed()` reports whether the
  caller should exit non-zero.
- `Options`: `OldPath`, `NewPath`, `RefsOnly`, `DeepRewrite` (the CLI's
  `--deep` flag: extends rewriting into narrative bodies such as session
  transcripts), `Reporter` (progress and warning sink, unused by `DryRun`;
  nil-handling follows `internal/progress/README.md` §Reporter injection).
- `Plan`: `ByTool []ToolPlan`. `ToolPlan`: `Tool`, `Absent` (true when the
  target reported `tool.ErrProjectAbsent`: it simply does not know this
  project, and `Surfaces` is empty rather than fabricated), `Surfaces
  []SurfaceCount`, `Warnings []string`.
- `ApplyResult`: `ByTool []ToolResult`. `ToolResult`: `Tool`, `Absent`,
  `Success`, `Err`, `Surfaces []SurfaceCount`, `Warnings []string`.
- `SurfaceCount`: `Name`, `Count`.

## Contracts

### Apply contract

Callers: `cc-port move --apply` command in `cmd/cc-port`. See
[`internal/lock/README.md`](../lock/README.md) §Concurrency guard for the
witness-then-flock ordering that guards `Apply`.

#### Handled

- Every selected target's `MoveSurfaces` and witness-plus-flock preflight
  run before any target applies. A `tool.ErrProjectAbsent` from
  `MoveSurfaces` marks that target `Absent` and skips it during apply, still
  holding its (already-acquired) lock through the full run for consistency
  with the other targets.
- Each target's surfaces apply in the order its adapter returned them, each
  registering its own rollback with a fresh `tool.Restorer`; a surface
  failure rolls back only that target's own `Restorer` (see
  `docs/architecture.md` §Crash and idempotence contract).
- Every acquired lock is released, in reverse acquisition order, via a single
  deferred cleanup that runs regardless of how `Apply` returns.

#### Refused

- A target's own encoded-directory collision and identity checks are the
  adapter's responsibility (see `internal/tool/claude/README.md` for the
  Claude instance); this package does not special-case any tool's refusal
  conditions.

#### Not covered

- Per-tool file shapes, categories, or residual-risk content. Those are
  described where the adapter that owns them documents its own `Surface`
  list.

## Tests

Unit tests in `move_test.go`, `preflight_test.go`, and `sweep_test.go`.
Coverage: `DryRun`/`Apply` across single- and multi-target sweeps, the
`tool.ErrProjectAbsent` absent-target path in both dry-run and apply,
preflight ordering (surfaces before lock, lock acquisition in registry
order), lock release in reverse order regardless of apply outcome, per-tool
failure isolation (`ApplyResult.Failed`), and residual-warning propagation
into both `Plan` and `ApplyResult`.

Per-adapter move behavior (which files move, malformed-history handling,
file-history preservation, source mtime preservation) is tested in each
adapter package; see `internal/tool/claude/README.md` §Tests for the Claude
instance and `internal/tool/codex/README.md` §Tests for the Codex instance.
