# internal/transport — agent notes

Zip-layout registry for session-keyed groups. See `README.md` for the full contract.

## Before editing

- Every entry in `SessionKeyedTargets` must be index-aligned with
  `claude.SessionKeyedGroups`; the alignment is not runtime-checked, only
  the unit test catches drift (README §Contracts).
- `ZipPrefix` values must stay unique and slash-terminated — the importer
  dispatches on prefix match (README §Contracts).

## Navigation

- Registry: `session_keyed_targets.go`.
- Alignment: `alignment_test.go`.

Read `README.md` before changing anything under `## Contracts`.
