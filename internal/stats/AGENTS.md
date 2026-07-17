# internal/stats — agent notes

## Before editing

- This package is generic orchestration; it must never import a tool adapter. Per-surface reference counting and disk sizing live in each adapter's `Auditor` methods (e.g. `internal/tool/claude`). (README §Metric scoping)
- Keep all-projects mode disk-only; references need a confirmed real path (README §Metric scoping).
- A target reporting `tool.ErrProjectAbsent` contributes a zero, `Absent: true` footprint; it must not fail the whole sweep (README §Metric scoping).
- Never size or read file-history snapshots as anything but opaque bytes (README §Disk-footprint categories).

## Navigation

- Entry points: `stats.go:ComputeFootprint`, `stats.go:ComputeAllFootprints`.
- Per-tool disk sizing and reference counting: `internal/tool/claude` (`stats.go`).
- Tests: `stats_test.go`.
