# internal/transport

Zip-layout descriptors for the five session-UUID-keyed groups. Both `internal/export` and `internal/importer` depend on these descriptors to agree on where each group's files live inside a portable archive and which `~/.claude/` base directory they restore to. The package exists as a neutral third module so neither sibling has to import the other.

## Contracts

- **Index alignment with `claude.SessionKeyedGroups`** — `SessionKeyedTargets[i].Group == claude.SessionKeyedGroups[i].Name` for every `i`, and the slice lengths are equal. The alignment is not runtime-checked; `alignment_test.go:TestSessionKeyedTargets_AlignedWithGroups` is the only guard against drift.
- **Unique, slash-terminated `ZipPrefix`** — every entry's `ZipPrefix` is unique across the registry and ends with `/`. The importer dispatches incoming zip entries by prefix match, so a duplicate or unterminated prefix silently miscategorises files. Enforced by `alignment_test.go:TestSessionKeyedTargets_ZipPrefixesUnique` and `TestSessionKeyedTargets_ZipPrefixesTerminatedWithSlash`.
- **`HomeBaseDir` semantics** — a zip entry whose name is `<ZipPrefix><rel>` restores to `filepath.Join(target.HomeBaseDir(home), rel)`. The base directory is computed from a `*claude.Home` so test overrides and real homes share one code path.
- **Canonical order is user-visible** — downstream consumers iterate `SessionKeyedTargets` in slice order for display and for deterministic archive layout. Reordering the slice is a user-visible change.

## Navigation

- Registry: `session_keyed_targets.go`.
- Alignment tests: `alignment_test.go`.
