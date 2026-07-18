# cc-port architecture

## Layout

```
cc-port/
├── cmd/cc-port/            CLI entry point: flag parsing, dispatch, exit codes, tool registry
├── internal/
│   ├── archive/            ZIP layout shared by every tool: <tool>/ prefixes, entry caps,
│   │                       os.Root containment, placeholder substitution
│   ├── encrypt/            Age encrypt and decrypt stages for the pipeline runner
│   ├── export/             Generic export orchestration: one archive across every target
│   ├── file/               Pipeline source/sink stages for local filesystem I/O
│   ├── fsutil/             Shared filesystem helpers: directory copy, path-ancestor resolution
│   ├── importer/           Generic import orchestration: preflight, staging, atomic promotion
│   ├── lock/               Advisory lock + live-writer refusal
│   ├── logo/               ASCII banner rendered when built with -tags logo (cc-port-with-logo binary)
│   ├── manifest/           metadata.xml wire DTOs + per-tool category validation
│   ├── move/               Generic move orchestration: per-tool surfaces, preflight, apply
│   ├── pipeline/           WriterStage/ReaderStage interfaces + composing runners
│   ├── progress/           Progress reporter, event stream, and four output renderers
│   ├── remote/             gocloud.dev-backed remote source and sink stages
│   ├── rewrite/            Byte-level and TOML rewrite primitives + SafeRenamePromoter
│   ├── scan/               Read-only scanner for ~/.claude/rules/*.md
│   ├── sqlrewrite/         SQL-level path rewriting on SQLite (busy_timeout=0, checkpoint discipline)
│   ├── stats/              Generic project-footprint orchestration (read-only)
│   ├── sync/               Push and pull orchestration: plan, execute, dry-run rendering
│   ├── testutil/           Test fixture helper
│   ├── tool/               The tool contract (Tool, Workspace, Surface, Restorer) + tool.Set registry
│   │   ├── claude/         Claude Code adapter: path encoding, locations, schemas, move/export/import/stats
│   │   └── codex/          OpenAI Codex adapter: SQLite + TOML + rollout JSONL, move/export/import/stats
│   └── ui/                 Interactive prompts (charm.land/huh v2)
├── integration_test.go     End-to-end CLI tests
└── testdata/dotclaude/     Minimal ~/.claude fixture for tests
```

Each non-trivial directory has a `README.md`. Directories with hard editing rules additionally carry an `AGENTS.md` (loaded by Claude Code via a one-line `CLAUDE.md`). The `README.md` is the developer narrative. The `AGENTS.md` is a pointer-only map into it.

`internal/claude` and `internal/transport` do not exist anymore. The Claude Code adapter moved to `internal/tool/claude` and absorbed `internal/transport`'s archive-layout registry into one merged `claude.Registries` slice (see §Registry source of truth). Command packages (`internal/move`, `internal/export`, `internal/importer`, `internal/stats`, `internal/sync`) import `internal/tool` only; they never import an adapter package. Only `cmd/cc-port` imports `internal/tool/claude` and `internal/tool/codex`, in `cmd/cc-port/tools.go`.

## The tool contract

cc-port is an N-tool porting engine. `internal/tool` defines a compile-time
contract every supported coding tool implements; `internal/tool/claude` and
`internal/tool/codex` are the two current adapters. Every other invariant in
this document and every command-layer README assumes the shapes described
here.

### Registry: a literal constructor call, not a plugin system

`cmd/cc-port/tools.go:newToolSet` is the one place cc-port lists its
supported tools:

```go
func newToolSet() *tool.Set {
	return tool.NewSet(claude.New(), codex.New())
}
```

There is no `init()` and no discovery mechanism. `tool.NewSet` validates the
registry once at construction (unique tool names, unique `(tool, category)`
pairs, unique implicit placeholder keys across tools) and panics on a
violation, because a collision there is a registry-construction bug in
`tools.go`, not an operational error a caller can recover from. `tool.Set`
then exposes `All`, `ByName`, `Detected`, and `ParseQualified` in
registration order.

### Adapter boundaries

Command packages (`internal/move`, `internal/export`, `internal/importer`,
`internal/stats`, `internal/sync`) import `internal/tool` and nothing more.
They drive every tool through the `Tool` and `Workspace` interfaces and
never branch on which tool they are talking to. Adapters
(`internal/tool/claude`, `internal/tool/codex`) import `internal/tool` plus
whatever shared substrate they need (`internal/rewrite`, `internal/sqlrewrite`,
`internal/archive`); only `cmd/cc-port` imports an adapter package directly.
A new tool adds one adapter package and one line in `tools.go`; it changes no
command package (see `internal/tool/README.md` §Extension rule for the full
checklist a third adapter follows).

`Tool` is the tool-level, connection-free half of the contract: `Name`,
`DisplayName`, `Categories`, `Detect`, `Open`, `ImplicitAnchorKeys`. `Open`
resolves the tool's state roots under its own rules (an explicit
`--<name>-home` override must already exist for Codex; Claude's home may be
lazily created) and returns a `Workspace` bound to them. `Workspace` composes
the four command-facing capabilities so one `Open` result serves every
command: `Mover` (`MoveSurfaces`, `ResidualWarnings`), `Exporter`
(`Placeholders`, `Export`), `Importer` (`PreflightDirs`, `ImplicitAnchors`,
`Stage`, `Finalize`), and `Auditor` (`ReferenceSurfaces`, `DiskCategories`,
`EnumerateProjects`), plus `Root`, `LockPath`, and `ActiveWriters` for
liveness evidence. A move's per-surface work is a `Surface`: a `Name`, a
`Plan` that reports a count without writing, and an `Apply` that performs the
rewrite and registers its rollback with a `Restorer` (see §Crash and
idempotence contract).

### Sweep semantics

Three sentinel errors govern how a multi-tool sweep treats one target that
cannot participate:

| Error | Meaning | Sweep behavior |
|---|---|---|
| `tool.ErrToolAbsent` | The tool has no state on this machine. | A tool named explicitly via `--tool` fails hard on it; an undetected tool in the default (no `--tool`) sweep is silently skipped by `cmd/cc-port/toolselect.go:resolveTargets`. |
| `tool.ErrProjectAbsent` | This tool has no record of the requested project. | Every command treats this as a legitimate empty result, not a failure: export writes an empty `<tool>` manifest block, move reports the target `Absent` with no surfaces, stats reports a zero, `Absent: true` footprint. |
| `tool.ErrNoWitness` | Liveness evidence could not be read (a witness source failed, not merely found nothing). | Blocks mutation exactly like a positive liveness result. An unreadable witness cannot be treated as "no writers"; refusing is the only safe response. |

`tool.ErrProjectAbsent` is deliberately not fatal because it is the common
case: a project workspace is not necessarily open in every installed tool,
and the sweep's job is to port what each tool actually knows.

### Construction seams

Adapters obtain environment lookups, process observation, and the clock
through constructor fields that default to real sources rather than free
in-line calls. Codex's `NewAdapter(getenv, listProcesses, now)` makes home
resolution (`$CODEX_SQLITE_HOME`), its witness's process-table scan, and its
freshness window testable without global mutation. Claude's
`NewAdapter(getenv, processLiveness, now)` routes default-home resolution and
per-session witness liveness through the same constructor seams; it checks the
specific PIDs named in session files rather than scanning a process table.
`New()` wires the real sources for production use.

## Contracts

One invariant per row; click through to the owning module for the full `Handled / Refused / Not covered` breakdown.

| Invariant                                               | Owner                                                                            |
|---------------------------------------------------------|----------------------------------------------------------------------------------|
| A compile-time registry maps `Tool` to its adapter; command packages never import an adapter | [`internal/tool/README.md`](../internal/tool/README.md) §Contract surface, §The tool contract |
| A multi-tool sweep skips `ErrToolAbsent`, empties on `ErrProjectAbsent`, refuses on `ErrNoWitness` | §Sweep semantics |
| Interactive prompts require a TTY                       | [`internal/ui/README.md`](../internal/ui/README.md)                              |
| Path substring rewrites respect component boundaries    | [`internal/rewrite/README.md`](../internal/rewrite/README.md)                    |
| SQLite path columns are rewritten through SQL, never byte-level | [`internal/sqlrewrite/README.md`](../internal/sqlrewrite/README.md) §Contracts |
| TOML project keys are rewritten byte-level with a parse-validate round trip | [`internal/rewrite/README.md`](../internal/rewrite/README.md) §TOML boundary rules |
| Archive entries carry a `<tool>/` namespace; decompression is capped and containment-checked | [`internal/archive/README.md`](../internal/archive/README.md) §Contracts |
| Project paths use a lossy encoding; collisions refused  | [`internal/tool/claude/README.md`](../internal/tool/claude/README.md)            |
| `~/.claude/rules/*.md` never rewritten in place         | [`internal/scan/README.md`](../internal/scan/README.md)                          |
| Rules-scan output flows as `scan.Report` bundle         | [`internal/scan/README.md`](../internal/scan/README.md)                          |
| Malformed `history.jsonl` lines preserved, not repaired | [`internal/tool/claude/README.md`](../internal/tool/claude/README.md) §Malformed history entries preserved |
| `history.jsonl` lines bounded at 16 MiB, oversized fail  | [`internal/tool/claude/README.md`](../internal/tool/claude/README.md) §History line cap |
| Archives are a closed placeholder contract              | [`internal/importer/README.md`](../internal/importer/README.md) §Import contract |
| Placeholder resolution composition (implicit anchors strongest, `--from-manifest` next, sender's own resolve weakest) | [`internal/importer/README.md`](../internal/importer/README.md) §Placeholder handling |
| Every export declares all of a tool's categories; unknown or missing refused | [`internal/manifest/README.md`](../internal/manifest/README.md) §Category manifest |
| Import writes are atomic with rollback across every tool's staged files | [`internal/importer/README.md`](../internal/importer/README.md) §Atomic staging  |
| A move's per-tool apply is a crash-safe, idempotent bracket; cross-tool rollback does not exist | §Crash and idempotence contract, [`internal/move/README.md`](../internal/move/README.md) §Apply contract |
| A `.git` object store inside a tool's state is never rewritten at the byte level | §Git-repo-in-state policy (cross-cutting) |
| Mutating commands lock + refuse during live writer activity | [`internal/lock/README.md`](../internal/lock/README.md)                          |
| Session-keyed user-wide directories follow the project  | [`internal/tool/claude/README.md`](../internal/tool/claude/README.md) §Project enumeration |
| User-wide files are rewritten via a polymorphic registry | [`internal/tool/claude/README.md`](../internal/tool/claude/README.md) §User-wide registry |
| Sync conflict-detection metadata stays inside the archive | [`internal/sync/README.md`](../internal/sync/README.md) §Plan-and-execute split |
| Cross-machine push refuses without `--force`              | [`internal/sync/README.md`](../internal/sync/README.md) §Plan-and-execute split |
| `--from-manifest` exclusivity with `--all` and per-category flags | [`cmd/cc-port/README.md`](../cmd/cc-port/README.md) §Category selection |
| Tempfile materialization for random-access consumers | [`internal/pipeline/README.md`](../internal/pipeline/README.md) §Public API |
| Layered AWS credential resolution (file > env > prompt) | [`internal/credentials/README.md`](../internal/credentials/README.md) §Source layering and precedence |
| Banner is consumer-defined; `internal/logo` is opt-in via `-tags logo` | `cmd/cc-port/banner_default.go`, `cmd/cc-port/banner_logo.go` |
| Reporter injected through Options, never package-global | [`internal/progress/README.md`](../internal/progress/README.md) §Reporter injection |
| Footprint reference counts match each surface's apply-rewrite variant | [`internal/tool/claude/README.md`](../internal/tool/claude/README.md) §Reference and disk accounting |

### TTY-prompt ownership split

Two modules own TTY-required prompts because the input shapes differ. `internal/ui` wraps `huh` forms (multi-field, banner, event loop); `internal/credentials` runs single-field echo-suppressed reads with a context-cancellation seam that closes `/dev/tty` on cancel. Neither module re-implements the other's path. New TTY work picks the owner whose shape matches; do not extend one to cover the other.

## Session-UUID-keyed user-wide data (cross-cutting)

Several `~/.claude/` directories carry per-session state belonging to a project:
`todos/<sid>-agent-<sid>.json`,
`usage-data/{session-meta,facets}/<sid>.json`,
`plugins/data/<ns>/<sid>/**`, and
`tasks/<sid>/**`. This set is a Claude Code concept: Codex has no equivalent
session-keyed user-wide directory. Each is enumerated through the project's
session-UUID set (the same set computed by `collectProjectDirEntries`). The
generic `internal/move`, `internal/export`, and `internal/importer` packages
have no knowledge of these directories; the Claude adapter's `Surface`,
`Export`, and `Stage`/`Finalize` implementations do:

- [`internal/tool/claude/README.md`](../internal/tool/claude/README.md)
  §Apply contract (move): rewrite + tracker rollback per surface, with
  `tasks/.lock` and `tasks/.highwatermark` excluded.
- [`internal/tool/claude/README.md`](../internal/tool/claude/README.md)
  §Session-keyed zip layout (export): opt-in via `--include claude/todos`,
  `--include claude/usage-data`, `--include claude/plugins-data`,
  `--include claude/tasks`, included in `--all`. Bodies pass through
  `sink.ApplyPlaceholders`.
- [`internal/importer/README.md`](../internal/importer/README.md) §Atomic
  staging: every tool's staged files, session-keyed or not, promote as one
  all-or-nothing batch. Promotion order follows archive entry order per tool,
  not a category-specific sequence.

### Registry source of truth

The canonical enumeration of Claude's storage surfaces is `claude.Registries`
(see [`internal/tool/claude/README.md`](../internal/tool/claude/README.md)
§Session-keyed registry and §User-wide registry). One `RegistryEntry` row
carries both the session-keyed file selector (`Files`) and the archive
layout (`ZipPrefix`, `HomeBaseDir`) that used to live in the separate
`internal/transport` package; `internal/transport` no longer exists. Every
consumer (the adapter's move surfaces, export, import staging, and CLI
renderers) iterates `claude.SessionKeyedGroups()` or
`claude.UserWideRewriteTargets()` instead of open-coding group names. Adding a
new session-keyed group means adding one `Registries` row and pointing its
`Category` at a name the adapter's `Categories()` declares.

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
rewrites snapshot contents. File-history is a Claude Code concept; Codex has
no equivalent, though the same never-inspect-opaque-bytes principle governs
Codex's memories worktree files and rollout narrative bodies (see §The tool
contract and §Git-repo-in-state policy).

Per-command handling, all owned by the Claude adapter now that `internal/move`,
`internal/export`, and `internal/importer` are generic:

- [`internal/tool/claude/README.md`](../internal/tool/claude/README.md)
  §File-history handling (move): left in place untouched (snapshots are keyed
  by session UUID, not by project path, so a move never needs to relocate
  them), with a residual-warning naming the preserved-verbatim count.
- [`internal/tool/claude/README.md`](../internal/tool/claude/README.md)
  §File-history handling (export): archive-verbatim, warning, privacy-of-exported-snapshots
  residual risk and the opt-out via omitting `--include claude/file-history`.
- [`internal/tool/claude/README.md`](../internal/tool/claude/README.md)
  §File-history handling (import): write-verbatim; placeholder resolution is
  skipped on file-history bodies.

## Per-session project subdirectories (cross-cutting)

Each `~/.claude/projects/<encoded>/<session-uuid>/` directory holds
per-session state that belongs to the project: `subagents/` agent
transcripts, `session-memory/`, and Workflow-tool output (run records and
`scripts/` under `workflows/`, per-agent transcripts under
`subagents/workflows/`). No command below special-cases these by kind. They
ride the same session-subdir handling as transcripts.

- `move` copies the whole encoded project directory, so the subtree
  relocates with it. The project-path rewrite reaches subdir bodies only
  under `--deep`, the same opt-in that covers subagent transcripts.
- `export` archives the subtree under the `sessions` category and `import`
  restores it from the `sessions/` prefix, with bodies anonymised on the way
  out and resolved on the way in. `push` and `pull` inherit this through the
  same export and import paths.

These bodies can also embed the encoded `~/.claude/projects/<encoded>/`
directory itself, for example in a run record's `scriptPath`. That reference
rides the same rewrite as the project path. `export` and `import` anonymise it
through `{{PROJECT_DIR}}`, and `move` swaps it, all keyed on the known absolute
path. The globals (`history.jsonl`, `sessions/*.json`, `.claude.json`) get no
dedicated encoded-dir pass on `move`, so they keep the residual handling
described in §File-history policy (cross-cutting).

## Git-repo-in-state policy (cross-cutting)

A tool's own state directory can itself contain a git repository. Codex's
memories directory (`$CODEX_HOME/memories/`) is the current instance: Codex
baselines it as a git worktree so it can diff and commit generated memory
files. Three rules govern any state git repository cc-port's rewrite surfaces
touch, in priority order:

1. Never rewrite bytes inside a `.git` object store. A byte-level substring
   replacement would corrupt git's own packed or loose object encoding.
2. When the owning tool provably re-initializes a missing `.git`, rewrite the
   worktree and delete `.git`. Deleting is safe only because the tool's own
   source-verified behavior recreates the baseline unconditionally on next
   use; this is not a general license to delete a git directory.
3. Otherwise, leave `.git` in place, rewrite the worktree, and warn. The
   worktree files still need their path rewritten; the repository state
   (commits, remotes, refs) is left untouched and the caller is told so.

Rule 2 applies to Codex's memories baseline behind a shape probe:
`memories/.git/config` exists and contains no `[remote` section. This shape
(a local-only baseline with no configured remote) is cc-port's own heuristic
standing in for the underlying fact that Codex's baseline helper
unconditionally re-initializes a missing or unusable `.git` on next write.
Any other shape (a `.git` carrying a `[remote` section, meaning a user or
tooling has attached the worktree to a real remote) falls back to rule 3:
`internal/tool/codex/README.md` §Git baseline handling reports it as a warning
rather than deleting it. No git dependency enters cc-port; the probe reads
`memories/.git/config` as a plain text file.

## Crash and idempotence contract

`cc-port move --apply` runs each selected tool's `Surface` list in order
(`internal/move/README.md` §Apply contract) and the same shape governs a
tool's own internal apply bracket. This section is the one place the
bracket's crash and idempotence guarantees are described; every module
README that implements a `Surface` points here instead of restating it.

### The apply bracket

Within one tool's apply: any surface whose storage is SQL opens its
transaction and registers its rollback with the `tool.Restorer` (via
`RegisterUndo`) before any other surface runs. `internal/tool/codex`'s
state-db and memories-db surfaces are the concrete instance, each calling
`sqlrewrite.Open`, beginning a transaction, and deferring `commit` to the
last surface. Every file surface in between is individually atomic: it
snapshots the file's pre-image with `Restorer.RegisterFile` before
overwriting it via a sibling-temp-and-rename (`rewrite.SafeWriteFile` or
`archive`'s equivalent). Directory promotion copies into a sibling staging
directory, registers its removal before the copy, and renames it into place
only after the copy succeeds. The same register-before-rename ordering
governs the git-baseline surface's `.git`-to-backup rename, including its
stranded-backup reconciliation on the next apply (`internal/tool/codex/README.md`
§Git baseline handling). The final surface in the list commits every open SQL
transaction and checkpoints each database afterward; no other surface
commits or checkpoints early.

### In-process failure

If any surface's `Apply` returns an error, `move.applyTarget` calls
`Restorer.Restore`, which walks every registration in reverse order: open SQL
transactions roll back and close, and every registered file is restored from
its saved pre-image. The tool's on-disk state returns to exactly what it was
before the apply attempt began.

### SIGKILL and re-run convergence

A `SIGKILL` at any point during apply leaves files possibly part-rewritten
and every open SQL transaction uncommitted: the transaction dies with the
process, so the database itself is untouched. Every surface is individually
idempotent: a rewritten file contains no remaining occurrences of the old
path, and a rewritten database row no longer matches the old path predicate.
Re-running the same move therefore converges to the same end state regardless
of where the previous attempt was killed. A leftover directory staging artifact
is removed before retrying, while a promoted destination that still has its
source resumes at source removal. Both properties (crash rollback,
re-run idempotence) are covered by adapter and move-package tests; this prose
is the one place the contract itself is described.

### No cross-tool rollback

A completed tool already reflects the true new path by the time a later
tool's apply fails. `move.Apply` records a per-tool success/failure result
rather than attempting to undo an already-succeeded tool, because undoing it
would require re-deriving the old state from data that has already been
correctly rewritten, and there is nothing to roll back to. The caller sees a
per-tool table and exits non-zero when any tool failed, with the failed
tool's own state left exactly as its own apply bracket guarantees (rolled
back to pre-apply, per §In-process failure).

### Two adjudicated residual windows

Two narrow windows are accepted trade-offs of the commit-order choice
described in §The apply bracket, not defects:

1. **The serial-commit window between the memories and state databases.**
   Two SQLite transactions cannot commit atomically against each other.
   `internal/tool/codex`'s commit surface commits the memories-database
   transaction before the state-database transaction, deliberately, because
   `threads.cwd` in the state database is the identity source project lookups
   key on. A failure between the two commits leaves the project still
   discoverable (memories committed, state not yet), and a re-run converges:
   the memories rewrite is idempotent, so re-applying it against
   already-rewritten rows is a no-op, and the state commit that failed
   previously is retried.
2. **The narrower multiple-state-generation edge inside that window.** Codex
   can carry more than one generation-suffixed `state_*.sqlite` file. If the
   memories commit succeeds, then the process is killed before every
   `state_*.sqlite` transaction commits, one state database's `threads.cwd`
   can end up correctly rewritten while another's `agent_jobs` path columns
   remain unrewritten. A project identity resolved from the first database
   plus an `agent_jobs` reference resolved from the second could reference a
   path that no longer round-trips consistently. This is accepted: the
   partial-commit error message names exactly which databases committed
   before the failure, so the operator has what they need to re-run the move
   and converge.

## Pipeline composition (cross-cutting)

cc-port's read and write paths compose through a uniform `WriterStage`
and `ReaderStage` interface owned by `internal/pipeline`. Every step on
either path satisfies one of the two interfaces. Source and sink stages
are degenerate cases of the same shapes (their upstream / downstream
parameter is the zero value). The runners `RunWriter` and `RunReader`
chain stages in a list, returning the outermost writer or final
`Source` to the consumer.

A writer stage's `Open` returns its writer plus an optional `io.Closer`. A reader stage's `Open` returns its `pipeline.View`, the `pipeline.Meta` it contributes, and an optional `io.Closer`. The runner accumulates non-nil closers and walks them on outer Close in chain order (writer) or reverse chain order (reader) with `errors.Join`. The reader runner also merges every stage's Meta contribution into `Source.Meta` as it walks the chain. Outer Close is idempotent. Stages do not chain to upstream or downstream Close.

Stages live in their owning packages:

| Stage | Package | Role |
|---|---|---|
| `file.Source`, `file.Sink` | `internal/file` | Local filesystem endpoints |
| `encrypt.WriterStage`, `encrypt.ReaderStage` | `internal/encrypt` | Age encrypt / decrypt filters (self-skipping) |
| `remote.Source`, `remote.Sink` | `internal/remote` | gocloud.dev-backed remote endpoints (file://, s3://) |
| `pipeline.MaterializeStage` | `internal/pipeline` | Terminal materialization to a 0600 tempfile when the downstream consumer needs `io.ReaderAt` |

`cmd/cc-port` owns ordering and any policy decisions (which stages to
include per invocation). The runner is policy-free.

Per-command pipelines:

- [`cmd/cc-port/export.go`](../cmd/cc-port/export.go) write path uses `[encrypt.WriterStage{Pass}, file.Sink]`. The encrypt stage self-skips when `Pass` is empty.
- [`cmd/cc-port/importcmd.go`](../cmd/cc-port/importcmd.go) read path uses `[file.Source, encrypt.ReaderStage{Pass, Mode: Strict}, pipeline.MaterializeStage]`. The reader stage owns the encrypted-vs-plaintext × pass-vs-no-pass dispatch internally. `MaterializeStage` short-circuits on local-file chains because `file.Source` already populates `ReaderAt`. `import manifest` reuses the same stage list.
- [`cmd/cc-port/pushcmd.go`](../cmd/cc-port/pushcmd.go) write path uses `[encrypt.WriterStage{Pass}, remote.Sink]`. The encrypt stage self-skips when `Pass` is empty.
- [`cmd/cc-port/pushcmd.go`](../cmd/cc-port/pushcmd.go) read path uses `[remote.Source, encrypt.ReaderStage{Pass, Mode: Permissive}, pipeline.MaterializeStage]` for the cross-machine probe. Permissive admits a plaintext prior; Strict is on pull. `MaterializeStage` drains the gocloud reader because `remote.Source` is streaming.
- [`cmd/cc-port/pullcmd.go`](../cmd/cc-port/pullcmd.go) read path uses `[remote.Source, downloadCounterStage, encrypt.ReaderStage{Pass, Mode: Strict}, pipeline.MaterializeStage]`. `downloadCounterStage` counts the encrypted bytes streamed off the remote once, as `MaterializeStage` drains them.

Materialization moved out of `remote.Source` and `encrypt.ReaderStage` into a dedicated terminal stage. Read paths are streaming by default. `MaterializeStage` short-circuits when the upstream View already exposes `ReaderAt` (local-file chains, `file.Source`) and otherwise drains to a 0600 tempfile owned by the runner's close cascade.

Future filters (compression, signing) plug in by adding new stage
types and including them in a command's stage list. The runner does
not change.
