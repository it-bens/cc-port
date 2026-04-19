# internal/transport

## Purpose

Zip-layout descriptors for the session-UUID-keyed groups. Both `internal/export` and `internal/importer` depend on these descriptors to agree on where each group's files live inside a portable archive and which `~/.claude/` base directory they restore to. The package exists as a neutral third module so neither sibling has to import the other.

Not a ZIP reader or writer — the struct only names the per-group layout shape; entry I/O lives in the export and import orchestrators.

## Public API

- `SessionKeyedTarget` — struct carrying `Group` (stable machine key, matches `claude.SessionKeyedGroup.Name`), `ZipPrefix` (unique, slash-terminated archive prefix), and `HomeBaseDir func(*claude.Home) string` (resolves the on-disk restore root from a `*claude.Home` so test overrides and real homes share one code path).
- `SessionKeyedTargets []SessionKeyedTarget` — the ordered registry; index-aligned with `claude.SessionKeyedGroups`.

## Contracts

### Archive-layout registry

- **Index alignment with `claude.SessionKeyedGroups`** — `SessionKeyedTargets[i].Group == claude.SessionKeyedGroups[i].Name` for every `i`, and the slice lengths are equal. The alignment is not runtime-checked; `alignment_test.go:TestSessionKeyedTargets_AlignedWithGroups` is the only guard against drift.
- **Unique, slash-terminated `ZipPrefix`** — every entry's `ZipPrefix` is unique across the registry and ends with `/`. The importer dispatches incoming zip entries by prefix match, so a duplicate or unterminated prefix silently miscategorises files. Enforced by `alignment_test.go:TestSessionKeyedTargets_ZipPrefixesUnique` and `TestSessionKeyedTargets_ZipPrefixesTerminatedWithSlash`.
- **`HomeBaseDir` semantics** — a zip entry whose name is `<ZipPrefix><rel>` restores to `filepath.Join(target.HomeBaseDir(home), rel)`. The base directory is computed from a `*claude.Home` so test overrides and real homes share one code path.
- **Canonical order is user-visible** — downstream consumers iterate `SessionKeyedTargets` in slice order for display and for deterministic archive layout. Reordering the slice is a user-visible change.

## Tests

`alignment_test.go` covers the three invariants above: index alignment with `claude.SessionKeyedGroups`, `ZipPrefix` uniqueness, and `ZipPrefix` slash-termination.

## Navigation

- Registry: `session_keyed_targets.go`.
- Alignment tests: `alignment_test.go`.
