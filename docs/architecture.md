# cc-port architecture

## Layout

```
cc-port/
├── cmd/cc-port/            CLI entry point (flag parsing, dispatch, exit codes)
├── internal/
│   ├── claude/             Claude Code data layout: path encoding, locations, schemas
│   ├── export/             Export orchestration: ZIP, manifest, path anonymisation
│   ├── file/               Pipeline source/sink stages for local filesystem I/O
│   ├── fsutil/             Shared filesystem helpers: directory copy, path-ancestor resolution
│   ├── importer/           Import orchestration: placeholder validation, atomic staging
│   ├── lock/               Advisory lock + live-session refusal
│   ├── logo/               ASCII banner rendered on interactive prompts
│   ├── manifest/           metadata.xml wire DTOs + category enum table
│   ├── move/               Move plan, dry-run, apply with copy-verify-delete
│   ├── pipeline/           WriterStage/ReaderStage interfaces + composing runners
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
| Every export declares all categories; unknown refused   | [`internal/manifest/README.md`](../internal/manifest/README.md) §Category manifest |
| Import writes are atomic with rollback                  | [`internal/importer/README.md`](../internal/importer/README.md) §Atomic staging  |
| Mutating commands lock + refuse during live sessions    | [`internal/lock/README.md`](../internal/lock/README.md)                          |
| Session-keyed user-wide directories follow the project  | [`internal/claude/README.md`](../internal/claude/README.md) §Project enumeration |
| User-wide files are rewritten via a polymorphic registry | [`internal/claude/README.md`](../internal/claude/README.md) §User-wide registry |

## Session-UUID-keyed user-wide data (cross-cutting)

Several `~/.claude/` directories carry per-session state belonging to a project:
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
  session-keyed prefix arms staged at sibling temps, promoted last in
  order so the most load-bearing data settles first.

### Registry source of truth

The canonical enumeration of session-keyed groups is
`claude.SessionKeyedGroups` (see
[`internal/claude/README.md`](../internal/claude/README.md) §Session-keyed
registry). Archive layout (zip prefix + import home base directory) lives in
`transport.SessionKeyedTargets`, index-aligned with `SessionKeyedGroups` and
verified by an alignment unit test in `internal/transport`. Every per-command
consumer (move, export, import, CLI renderers) iterates these registries
instead of open-coding group names. Adding a new session-keyed group
means editing both slices in the same commit; `internal/move`'s
`planCategories` derives from `SessionKeyedGroups` and updates automatically.

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

## Pipeline composition (cross-cutting)

cc-port's read and write paths compose through a uniform `WriterStage`
and `ReaderStage` interface owned by `internal/pipeline`. Every step on
either path satisfies one of the two interfaces. Source and sink stages
are degenerate cases of the same shapes (their upstream / downstream
parameter is the zero value). The runners `RunWriter` and `RunReader`
chain stages in a list, returning the outermost writer or final
`Source` to the consumer.

Stages live in their owning packages:

| Stage | Package | Role |
|---|---|---|
| `file.Source`, `file.Sink` | `internal/file` | Local filesystem endpoints |
| `encrypt.WriterStage`, `encrypt.ReaderStage` | `internal/encrypt` | Age encrypt / decrypt filters (self-skipping) |

`cmd/cc-port` owns ordering and any policy decisions (which stages to
include per invocation). The runner is policy-free.

Per-command pipelines:

- [`cmd/cc-port/export.go`](../cmd/cc-port/export.go) write path uses `[encrypt.WriterStage{Pass}, file.Sink]`. The encrypt stage self-skips when `Pass` is empty.
- [`cmd/cc-port/importcmd.go`](../cmd/cc-port/importcmd.go) read path uses `[file.Source, encrypt.ReaderStage{Pass, Mode: Strict}]`. The reader stage owns the encrypted-vs-plaintext × pass-vs-no-pass dispatch internally. `import manifest` reuses the same stage list.

Future filters (sync source/sink, compression, signing) plug in by
adding new stage types and including them in a command's stage list.
The runner does not change. The sync spec adds remote source and sink
stages on top of these file and encrypt stages.
