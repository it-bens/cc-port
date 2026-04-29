# internal/sync -- agent notes

## Before editing

- Plan functions are pure reads and never mutate remote or local state. Execute owns mutation. Do not blur the boundary (README §Plan-and-execute split).
- Conflict-detection metadata lives inside `metadata.xml` inside the archive. Never use bucket-level custom metadata for these fields (README §Plan-and-execute split).
- `selfPusher` derives identity per-invocation; never persist it (README §Public API).

## Navigation

- Entry: `sync.go` (types, sentinels, selfPusher, Plan/Execute stubs).
- Tests: `sync_test.go`.
