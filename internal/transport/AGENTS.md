# internal/transport — agent notes

## Before editing

- Every entry in `SessionKeyedTargets` must be index-aligned with `claude.SessionKeyedGroups` (README §Archive-layout registry).
- `ZipPrefix` values must stay unique and slash-terminated (README §Archive-layout registry).
- Slice order is user-visible; reordering changes display output and archive layout (README §Archive-layout registry).

## Navigation

- Registry: `session_keyed_targets.go`.
- Alignment: `alignment_test.go`.
