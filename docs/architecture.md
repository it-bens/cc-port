# cc-port architecture

## Layout

```
cc-port/
├── cmd/cc-port/            CLI entry point (flag parsing, dispatch, exit codes)
├── internal/
│   ├── claude/             Claude Code data layout: path encoding, locations, schemas
│   ├── export/             Export orchestration: ZIP, manifest, path anonymisation
│   ├── fsutil/             Shared filesystem helpers: directory copy, path-ancestor resolution
│   ├── importer/           Import orchestration: placeholder validation, atomic staging
│   ├── lock/               Advisory lock + live-session refusal
│   ├── manifest/           metadata.xml wire DTOs + nine-category enum table
│   ├── move/               Move plan, dry-run, apply with copy-verify-delete
│   ├── rewrite/            Byte-level rewrite primitives + SafeRenamePromoter
│   ├── scan/               Read-only scanner for ~/.claude/rules/*.md
│   ├── testutil/           Test fixture helper
│   ├── transport/          ZIP layout registry for session-keyed groups
│   └── ui/                 Interactive prompts (charm.land/huh v2)
├── integration_test.go     End-to-end CLI tests
└── testdata/dotclaude/     Minimal ~/.claude fixture for tests
```

Each non-trivial directory has a `README.md`. Directories with hard editing rules additionally carry an `AGENTS.md` (loaded by Claude Code via a one-line `CLAUDE.md`). The `README.md` is the developer narrative. The `AGENTS.md` is a pointer-only map into it.

## Contracts

One invariant per row; click through to the owning module for the full `Handled / Refused / Not covered` breakdown.

| Invariant                                               | Owner                                                                            |
|---------------------------------------------------------|----------------------------------------------------------------------------------|
| Interactive prompts require a TTY                       | [`internal/ui/README.md`](../internal/ui/README.md)                              |
| Path substring rewrites respect component boundaries    | [`internal/rewrite/README.md`](../internal/rewrite/README.md)                    |
| Project paths use a lossy encoding; collisions refused  | [`internal/claude/README.md`](../internal/claude/README.md)                      |
| `~/.claude/rules/*.md` never rewritten in place         | [`internal/scan/README.md`](../internal/scan/README.md)                          |
| Malformed `history.jsonl` lines preserved, not repaired | [`internal/move/README.md`](../internal/move/README.md)                          |
| `history.jsonl` lines bounded at 16 MiB, oversized fail  | [`internal/claude/README.md`](../internal/claude/README.md) §History line cap    |
| Archives are a closed placeholder contract              | [`internal/importer/README.md`](../internal/importer/README.md) §Import contract |
| Every export declares all 9 categories; unknown refused | [`internal/manifest/README.md`](../internal/manifest/README.md) §Category manifest |
| Import writes are atomic with rollback                  | [`internal/importer/README.md`](../internal/importer/README.md) §Atomic staging  |
| Mutating commands lock + refuse during live sessions    | [`internal/lock/README.md`](../internal/lock/README.md)                          |
| Session-keyed user-wide directories follow the project  | [`internal/claude/README.md`](../internal/claude/README.md) §Project enumeration |

## Session-UUID-keyed user-wide data (cross-cutting)

Five `~/.claude/` directories carry per-session state belonging to a project:
`todos/<sid>-agent-<sid>.json`,
`usage-data/{session-meta,facets}/<sid>.json`,
`plugins/data/<ns>/<sid>/**`, and
`tasks/<sid>/**`. Each is enumerated through the project's session-UUID set (the
same set computed by `collectProjectDirEntries`). Per-command handling lives in
the relevant module:

- [`internal/move/README.md`](../internal/move/README.md) §Apply contract: copy
  + rewrite + tracker rollback, with `tasks/.lock` and `tasks/.highwatermark`
  excluded.
- [`internal/export/README.md`](../internal/export/README.md) §Session-keyed zip layout:
  opt-in via `--todos`, `--usage-data`, `--plugins-data`, `--tasks`, included
  in `--all`. Bodies pass through `applyPlaceholders`.
- [`internal/importer/README.md`](../internal/importer/README.md) §Atomic staging:
  4 new prefix arms staged at sibling temps, promoted last in the
  order so the most load-bearing data settles first.

### Registry source of truth

The canonical enumeration of session-keyed groups is
`claude.SessionKeyedGroups` (see
[`internal/claude/README.md`](../internal/claude/README.md) §Session-keyed
registry). Archive layout (zip prefix + import home base directory) lives in
`transport.SessionKeyedTargets`, index-aligned with `SessionKeyedGroups` and
verified by an alignment unit test in `internal/transport`. Every per-command
consumer (move, export, import, CLI renderers) iterates these registries
instead of open-coding the five group names. Adding a sixth session-keyed
group means editing both slices in the same commit plus one entry in
`internal/move`'s `planCategories`.

`~/.claude/teams/<team>/**` is intentionally NOT in this set. Team directories
are user-wide workspaces with no inspectable per-project attribution.

## File-history policy (cross-cutting)

cc-port treats every file under `~/.claude/file-history/<session-uuid>/`
as an opaque byte stream. The directory is indexed by session UUID (not
by project path), and each `<hash>@vN` entry is a verbatim copy of a
file the user edited through Claude Code. The in-session rewind feature
uses it by filename, not by content. Any project-path string that
appears inside a snapshot body is coincidental (log line, comment,
string literal) and not load-bearing, so cc-port never inspects or
rewrites snapshot contents.

Per-command handling:

- [`internal/move/README.md`](../internal/move/README.md) §File-history handling (move): copy-verbatim, stderr warning, stale-path-strings residual risk.
- [`internal/export/README.md`](../internal/export/README.md) §File-history handling (export): archive-verbatim, stderr warning, privacy-of-exported-snapshots residual risk and the `--file-history=false` opt-out.
- [`internal/importer/README.md`](../internal/importer/README.md) §File-history handling (import): write-verbatim, `ResolvePlaceholders` no-op detail on current archives.
