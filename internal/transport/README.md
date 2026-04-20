# internal/transport

## Purpose

Zip-layout descriptors for the five session-UUID-keyed groups. This package is a neutral third module so neither `internal/export` nor `internal/importer` has to import the other.

This package is not a ZIP reader or writer. The struct names the per-group layout shape; entry I/O lives in the export and import orchestrators.

## Public API

- `SessionKeyedTarget`: struct with `Group` (stable machine key, matches `claude.SessionKeyedGroup.Name`), `ZipPrefix` (unique, slash-terminated archive prefix), and `HomeBaseDir func(*claude.Home) string`. A zip entry named `<ZipPrefix><rel>` restores to `filepath.Join(target.HomeBaseDir(home), rel)`. Using a function rather than a string lets test overrides and real homes share one code path.
- `SessionKeyedTargets []SessionKeyedTarget`: the ordered registry, index-aligned with `claude.SessionKeyedGroups`.

## Contracts

### Archive-layout registry

Called by `internal/export` and `internal/importer`.

#### Handled

`SessionKeyedTargets[i].Group == claude.SessionKeyedGroups[i].Name` for every `i`, and the slice lengths are equal. The alignment is not runtime-checked. `alignment_test.go:TestSessionKeyedTargets_AlignedWithGroups` is the only guard against drift.

Every entry's `ZipPrefix` is unique across the registry and ends with `/`. The importer dispatches incoming zip entries by prefix match, so a duplicate or unterminated prefix silently miscategorizes files. `TestSessionKeyedTargets_ZipPrefixesUnique` and `TestSessionKeyedTargets_ZipPrefixesTerminatedWithSlash` enforce this.

#### Refused

None at runtime. This package defines read-only descriptors and rejects no input. An entry added to `SessionKeyedTargets` without a matching entry in `claude.SessionKeyedGroups` is not caught at compile time.

#### Not covered

Slice order is user-visible: downstream consumers iterate `SessionKeyedTargets` in slice order for display and for deterministic archive layout. Nothing prevents reordering the slice. Reordering is a user-visible change.

## Tests

`alignment_test.go` covers the three invariants above.

## Navigation

- Registry: `session_keyed_targets.go`.
- Alignment tests: `alignment_test.go`.
