# internal/stats — agent notes

## Before editing

- Route every reference count through `rewrite.CountPathInBytes` or `CountPathInBytesWithJSONEscape`; never `strings.Count` on a user path (README §Per-surface count variant).
- Match each surface's count variant to what an apply rewrites there, and run the encoded-dir second pass on transcripts and memory (README §Per-surface count variant).
- Keep all-projects mode disk-only; references need a confirmed real path (README §Metric scoping).
- Derive disk category keys from `manifest.AllCategories`; never hard-code a parallel list (README §Disk-footprint categories).
- Cap any `history.jsonl` line scan with `claude.MaxHistoryLine` (internal/claude/README.md §History line cap).
- Never size or read file-history snapshots as anything but opaque bytes (README §Disk-footprint categories).

## Navigation

- Entry points: `stats.go:ComputeFootprint`, `stats.go:ComputeAllFootprints`.
- Reference counting: `references.go`.
- Disk sizing: `disk.go`.
- Tests: `stats_test.go`.
