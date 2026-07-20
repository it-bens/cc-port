# internal/tool/codex

## Purpose

Implements `tool.Tool` and `tool.Workspace` for OpenAI Codex. Codex stores
project-associated state in shapes Claude Code never uses: verbatim absolute
`cwd` strings, a WAL-mode SQLite index with a live desktop writer, TOML
tables keyed by project path, JSONL session files that Codex may compress,
and a git-baselined memory directory. This adapter concentrates every one of those
tool-specific facts in this package; `internal/move`, `internal/export`,
`internal/importer`, and `internal/stats` know nothing about Codex.

## Public API

- `Adapter`, `New() *Adapter`: wired to the real environment and process
  table. `NewAdapter(getenv, listProcesses) *Adapter`: same shape with every
  seam explicit, for tests.
- `Home`: `Dir`, `SQLiteDir`, `AgentsDir`.
- `Workspace`, `NewWorkspace(home, getenv, listProcesses) *Workspace`: tests
  supply a fake getenv or process lister so they never touch the live
  process table.
- `ProcessLister func() ([]ProcessInfo, error)`, `ProcessInfo{PID, Name}`: the
  process-enumeration seam; production default is `listSystemProcesses`
  (shells out to `ps -Ao pid=,comm=`), darwin/linux only.
- `SetupFixture(t *testing.T) *Home`, `FixtureProjectPath() string`,
  `FixtureAgentsDir(t *testing.T) string`: the adapter-local test fixture
  helpers (see §Tests).

Every `tool.Tool` and `tool.Workspace` method is implemented but not
re-declared here; see `internal/tool/README.md` §Public API for the contract
shapes themselves.

## Contracts

### Home resolution

**Handled.**

- `Home.Dir` is `$HOME/.codex` by default, or an explicit `--codex-home`
  override. Unlike Claude's lazily-created home, an override must already
  exist, be a directory, and canonicalize (`canonicalizeExistingDir`); `Open`
  reports `tool.ErrToolAbsent` for a missing default location rather than
  fabricating a `Workspace` over state that was never written.
- `Home.SQLiteDir` mirrors Codex's three-tier resolution
  (`core/src/config/mod.rs:3669-3674`): the `sqlite_home` key in
  `config.toml`, then `$CODEX_SQLITE_HOME`, then the home directory itself.
  A relative `sqlite_home` value resolves against the home directory; a
  relative `$CODEX_SQLITE_HOME` resolves against the process's current
  working directory, matching Codex's own `resolve_sqlite_home_env`.
- `Home.AgentsDir` is `$HOME/.agents`, populated only when `$HOME` resolves;
  every surface rooted there activates only when the directory exists on
  disk.
- `profileSQLiteHomeWarning` checks every discovered `<profile>.config.toml`
  overlay for a `sqlite_home` different from the resolved `Home.SQLiteDir`.
  For a project this adapter already knows, both `ResidualWarnings` (move)
  and `Export` call it and add its result to their warnings, so a divergent
  overlay is reported rather than silently trusted. See Not covered for the
  paths that still resolve against base config.toml with no warning, and
  for why no path resolves against the overlay instead.
- `projectAbsenceError` covers the case a warning cannot reach: a project
  this adapter finds nowhere under the base-resolved directory. Every guard
  that would otherwise report a bare `tool.ErrProjectAbsent`
  (`Placeholders`, `Export`, `ReferenceSurfaces`, `DiskCategories`, and
  `MoveSurfaces`) calls it first. When a profile overlay declares a
  divergent `sqlite_home`, it returns `ErrProjectAbsenceUnresolved`
  instead, naming the overlay and the base directory checked. That error
  does not match `errors.Is(err, tool.ErrProjectAbsent)`, so
  move/export/stats sweep semantics treat it as a hard failure rather than
  silently skipping Codex. `ActiveWriters` is genuinely exempt: it never
  answers whether a particular project exists. `EnumerateProjects` is
  exempt only from this project-specific guard, since it too takes no
  project argument; it remains subject to the same base-only resolution
  limit as every other surface (see Not covered), so it can still omit a
  profile-only project from an all-project listing with no warning. When
  no overlay diverges, all five guarded call sites behave exactly as
  before.

**Refused.**

- An explicit `--codex-home` that does not exist, is not a directory, or
  cannot resolve through `filepath.EvalSymlinks`: `Open` returns an error
  before constructing a `Workspace`.

**Not covered.**

- A `sqlite_home` value that itself does not exist. Resolution only computes
  the path; database discovery (`discoverDatabases`) separately treats a
  missing directory as "no databases found," not an error.
- Resolving `Home.SQLiteDir` against the profile a past Codex session
  actually used. Codex selects a profile-v2 overlay only from the runtime
  `--profile` flag (`config/src/state.rs:38-53`,
  `core/src/config/mod.rs:1755-1763`, `resolve_profile_v2_config_path`) and
  persists neither the profile name nor its resolved `sqlite_home` anywhere
  Codex itself reads back: not in `config.toml`'s own `profile` key, an
  unrelated legacy mechanism Codex 0.144.5 refuses to start with at all
  when present (`core/src/config/mod.rs:3047-3054`); not in any
  `state/migrations/*.sql` column; and not in `SessionMeta` or
  `TurnContextItem` (`protocol/src/protocol.rs:3014-3062,3209-3252`). No
  later tool can determine which profile, if any, wrote the state on disk,
  so `Home.SQLiteDir` always resolves against base `config.toml`, matching
  Codex's own behavior with no `--profile` flag.
- Warning about a divergent profile overlay for a project this adapter
  already knows, anywhere but move and export. `ReferenceSurfaces` and
  `DiskCategories` (stats) add no per-call warning for a known project's
  possibly-incomplete data: `tool.Auditor`'s three methods return counts,
  sizes, and project listings, with no channel to warn through on success.
  `ActiveWriters`'s `busyProbeWitness` is not project-scoped at all: it
  probes every database discovered under `Home.SQLiteDir` regardless of
  project, and `tool.Workspace.ActiveWriters` returns writers and an error
  with the same no-warning shape. A divergent profile is silent in both
  cases as long as the project stays otherwise known.
- `EnumerateProjects` carries the same base-only resolution limit in a
  worse shape. It builds its candidate project set from
  `discoverDatabases(Home.SQLiteDir, ...)` thread cwds and
  `discoverConfigTOMLFiles` project keys alone, so a project known only
  through a thread row under a divergent profile's `sqlite_home` never
  becomes a candidate: it is missing from the listing entirely, not
  reported incomplete. `EnumerateProjects` also forwards whatever error
  `DiskCategories` returns for any one candidate project
  (`export_import_stats.go:1081`) without scoping the failure to that
  project, so one project's lower-level read failure aborts the whole
  listing. All three cases above are a deliberate residual, not an
  oversight: inferring the active profile instead (the sole overlay, or
  the most recently modified one) would silently inspect a directory that
  may be wrong, the exact failure this section exists to avoid.

### Glob, don't pin

**Handled.**

- Every database discovery site globs a generation-suffixed pattern
  (`state_*.sqlite`, `memories_*.sqlite`, `goals_*.sqlite`, `logs_*.sqlite`)
  rather than a literal filename, because Codex's own generation suffix can
  bump (`state_5.sqlite` today; a future binary may write `state_6.sqlite`,
  per `state/src/lib.rs:97-100`). `discoverDatabases` returns every match in
  sorted order; every move surface, count, and stats method iterates that
  full match set rather than assuming exactly one file per family.
- The fixture builder deliberately writes `state_5.sqlite` and
  `memories_1.sqlite` (see §Tests) specifically so a test that pinned a
  filename would still pass by coincidence while a real drift would not; the
  discovery code path is what globs, not the fixture name.

**Refused.**

- Pinning a literal database filename anywhere in this package's production
  code. If a future site needs a specific database, it globs and picks by a
  documented rule, not by a hard-coded name.

**Not covered.**

- Predicting future generation-suffix values. The glob pattern accepts any
  suffix; nothing in this package infers what the next generation number
  will be.

### Both-roots coverage

**Handled.**

- Rollouts live under two physical roots: `sessions/YYYY/MM/DD/` and the flat
  `archived_sessions/` (`rollout/src/lib.rs:21-22`); archiving physically
  renames the file from one root to the other
  (`thread-store/src/local/archive_thread.rs:41-53`). `rolloutRoots` walks
  both roots for `discoverRolloutFiles`, so every rollout surface (move
  rewrite, export, residual scanning) sees the same combined file set
  regardless of which root a given rollout currently sits under.
- `discoverRolloutFiles` returns one file per LOGICAL rollout: when both
  `X.jsonl` and a crash-window `X.jsonl.zst` sibling exist, only the plain
  file is kept, mirroring Codex's own walker
  (`rollout/src/compression.rs:141-163,941-943`). Move rewrite, export,
  `projectRollouts`, `knowsProject`, and stats all consume this deduplicated
  form.
- After sibling suppression, `discoverRolloutFiles` refuses every remaining
  `.zst` rollout by name with `ErrCompressedRolloutUnsupported`; discovery
  does not read compressed bytes.
- Export preserves the root distinction in the archive path
  (`archiveRolloutName` maps `sessions/` and `archived_sessions/` to
  `codex/sessions/…` and `codex/archived-sessions/…`); import stages back to
  the matching root, so an archived thread's location-derived archived state
  survives the round trip.

**Refused.**

- A rollout surface that walks only one root. Both roots are walked
  unconditionally; there is no flag to restrict to one.

**Not covered.**

- Detecting a rollout that exists under both roots simultaneously (a
  duplicate). Codex's own archive operation renames rather than copies, so
  this should not occur; this adapter does not defend against it.

### Witness evidence order

**Handled.**

- `ActiveWriters` collects both sources regardless of either outcome, so a
  dry-run reports every signal at once: a process-table match for `codex`,
  `codex-tui`, or `codex-app-server`; and `SQLITE_BUSY` on a `BEGIN
  IMMEDIATE` probe against each discovered database.
- If either source cannot be consulted, `ActiveWriters` returns an error
  wrapping `tool.ErrNoWitness`. Mutation treats that failure like positive
  liveness evidence rather than assuming there are no writers.

**Not covered.**

- A cooperative shutdown protocol. Detection is evidence only; the actual
  database write is separately protected by `sqlrewrite`'s `busy_timeout=0`.

### Era-A rollout handling

**Handled.**

- A rollout with no `session_meta` or `turn_context` line (`hasStructuredCwd`
  returns false) predates structured cwd tracking. Move skips it entirely,
  under `--deep` or not, since there is nothing to anchor a safe rewrite to;
  `ResidualWarnings` reports the count. Export cannot associate such a
  rollout with any project, so it is counted in `Skipped` and named in a
  warning rather than silently dropped from the archive.

**Refused.**

- Rewriting era-A rollout bodies under any flag. `--deep` extends rewriting
  into narrative bodies of structured rollouts; it does not create structure
  in an unstructured one.
- A `.zst` rollout with no plain sibling. Discovery refuses the operation by
  filename; a crash-window `X.jsonl` plus `X.jsonl.zst` pair still selects the
  plain file.

**Not covered.**

- Recovering an era-A rollout's project association by any other means (file
  path, directory listing). Codex itself cannot read these files back into a
  structured association either; the adapter matches that limitation rather
  than inventing a heuristic Codex does not use.

### History and session-index append-only

**Handled.**

- `Finalize` appends new lines to `history.jsonl` and `session_index.jsonl`
  through the shared `appendLinesToFile` helper, which opens each file with
  `O_APPEND` (`os.O_RDWR|os.O_CREATE|os.O_APPEND`) and never renames or
  replaces it. `appendUniqueHistory` deduplicates by `(session_id,
  timestamp, text)`. Codex timestamps `history.jsonl` at whole-second
  precision (`message-history/src/lib.rs:121-125`), so two distinct prompts
  submitted to one thread within the same wall-clock second need `text` in
  the key to survive as separate lines instead of collapsing into one on
  import. `appendUniqueExact` deduplicates by exact line match. Both
  scan the existing file first (`scanLines`), so re-importing the same
  archive never appends a duplicate line.
- For `history.jsonl`, never replacing the file is load-bearing: Codex's own
  `message-history` crate takes a real advisory file lock on append
  (`history_file.try_lock()`) and caches a `(log_id, offset)` pair keyed on
  the file's inode (`log_identity`, `metadata.ino()` on Unix,
  `message-history/src/lib.rs:425-429`) to serve the TUI's up-arrow history;
  a rename-replace would change the inode and invalidate that cache
  mid-session.
- For `session_index.jsonl`, the inode-cache rationale does not apply:
  Codex's own writer holds only a process-local mutex, not a file lock
  (`SESSION_INDEX_LOCK`, `rollout/src/session_index.rs:20`), and its reader
  re-opens the file and scans from the end on every lookup, with no
  persisted offset to invalidate. Appending in place still matters here for
  a different reason: with no shared lock, a temp-and-rename rewrite built
  from a snapshot could silently drop a Codex append that landed between
  the snapshot and the rename; `O_APPEND` cannot lose an already-committed
  line that way.

**Refused.**

- A temp-and-rename rewrite of either file for deduplication or any other
  bulk edit. `appendLinesToFile` is the only write path into either file and
  has no truncate-and-rewrite mode.

**Not covered.**

- Taking Codex's own lock before appending. Neither file's import path
  acquires a lock; `O_APPEND`'s atomic-write-at-EOF behavior, not explicit
  coordination with Codex's writer, is what keeps a concurrent Codex append
  intact.

### Sidecar update-only rationale

**Handled.**

- Export writes `codex/threads-sidecar.jsonl`, one line per exported thread
  carrying `archived_at`, `title`, and git fields that are otherwise
  DB-only (not dual-encoded in any file). Import applies each line via
  `sqlrewrite.UpdateColumnsByKey` against the destination's `threads` table,
  by primary key, and reports the count that could not be applied because no
  matching thread row exists yet.
- No `INSERT` ever targets the state database from the sidecar path. The
  state database is a foreign, self-healing, derived cache to this adapter,
  not its own primary store: Codex's own reconciler (stale-row deletion,
  startup backfill, fast-path read-repair, full reconciliation) rebuilds
  `threads` rows from rollout files independently, and an inserted row would
  fight that reconciler rather than cooperate with it. This rationale is
  scoped to foreign self-healing caches specifically; an adapter
  reconstituting its own primary SQL store performs `INSERT`s as expected new
  work on a connection that reuses `sqlrewrite.Open`'s safety envelope.
- Rows for imported rollouts become applicable only after a cwd-filtered
  listing touches them (for example, `codex resume` inside the project,
  which is how Codex populates `threads` from the rollout file in the first
  place); re-running the import afterward applies the remainder, and the
  import warning says exactly that.

**Refused.**

- An `INSERT` path for sidecar rows under any condition. `UpdateColumnsByKey`
  is structurally update-only (see `internal/sqlrewrite/README.md`
  §Update-only mutation); there is no sidecar code path that constructs an
  `INSERT`.

**Not covered.**

- Guaranteeing every sidecar row applies on the first import. A thread whose
  row Codex has not yet created is expected to need the documented re-run.

### cwd matching

**Handled.**

- Codex records `config.cwd()` verbatim and uncanonicalized
  (`rollout/src/recorder.rs`). Its normalizer,
  `normalize_for_native_workdir`, and its comparator,
  `paths_match_after_normalization`, both live in
  `utils/path-utils/src/lib.rs`: the former is a no-op except on Windows,
  the latter falls back to a literal comparison of the two original paths
  when either side's `canonicalize()` fails, with no lexical-clean step.
  cc-port resolves its project argument via
  `fsutil.ResolveExistingAncestor`, a full `filepath.EvalSymlinks`;
  without it, a symlink-aliased project directory left its rollout, thread
  row, and agent-job references invisible to export, stats, and move.
  cc-port's own comparator, `canonicalizePath`, resolves symlinks when a
  path exists and falls back to `filepath.Clean` otherwise (spec §5.1); a
  stored cwd with unresolved `..` can therefore compare differently under
  the two fallbacks, accepted because cc-port's fallback behaves like a
  real path rather than an opaque byte string. `pathMatchesProject`
  canonicalizes both operands before the existing
  equality-or-`/`-boundary-prefix rule, fixing every rollout- and
  config-key-matching call site at once: `identityMatchesProject` and
  `configTOMLKnowsProject` both route through it.
- `threads.cwd` matching moves the same rule into Go, since symlink
  resolution cannot run as a SQL predicate: `matchingThreadCWDs` fetches
  every distinct stored value under `COLLATE BINARY` (blocking a
  case-insensitive collation from folding byte-different values together)
  and canonicalizes each. `stateDBFileKnowsProject`,
  `countStateDBReadOnly`, `countThreadRows`, `projectThreadIDs`, and the
  move rewrite all derive their matched-value set from this one function,
  so a dry-run count and an apply never diverge; parity is algorithmic,
  not temporal, since count and rewrite open separate connections at
  separate times and a concurrent writer or a retargeted symlink can still
  shift the matched set between them. `COLLATE BINARY` equality or prefix
  SQL alone cannot drive the rewrite either: `matchingThreadRewrites`
  computes the canonical match in Go, and `sqlrewrite.UpdateColumnsByKey`
  rewrites each matched row by primary key, preserving the original
  suffix, the path past the project boundary, from the canonical forms
  rather than literal byte offsets.
- A rollout's own recorded `payload.cwd` needs the same fix:
  `rewriteRolloutLine` matches literal bytes via `internal/rewrite`, so a
  symlink-aliased rollout's stored cwd never contained oldPath's literal
  bytes to find. `rolloutSubstitutionSources` derives, from the rollout's
  session_meta/turn_context cwd values, every stored value canonically
  matching the project; `rolloutSubstitutions` pairs each with the value
  it rewrites to (`newPath`, plus whatever suffix a symlink-aliased
  value's canonical form carried past the project boundary).
  `planRolloutFile` and `MoveSurfaces`' preflight (`captureMovePreflight`)
  derive their source list from the same function on their own read of the
  rollout, so a symlink-aliased rollout gets rewritten instead of left
  stale.
- `matchingThreadCWDs` and `countMatchingThreadRows` are `threads.cwd`'s
  instances of `matchingColumnValues` and `countMatchingColumnRows`;
  `codexDevWarning` reuses those generic functions for `codex-dev.db`'s
  `local_thread_catalog.cwd` and `automation_runs.source_cwd`. Two call
  sites carry a real request context rather than `context.Background()`
  and check `ctx.Err()` per row: the `stateDBSurfaceWithPlans` Plan path
  (`countStateDB`) and the export/stats path (`countThreadRows`,
  `projectThreadIDs`, `projectThreadIDSet`). `matchingThreadRewrites`
  checks `ctx.Err()` too, but its sole caller,
  `stateDBRewritePlansForProject`, runs from `MoveSurfaces`' own preflight
  with `context.Background()` (`MoveSurfaces` itself takes no context), so
  it is never cancellable.

**Refused.**

- Widening the match breadth beyond the existing
  equality-or-`/`-boundary-prefix rule. cc-port already matches
  subdirectories under a project's cwd, a documented deviation from
  Codex's own strict-equality `paths_match_after_normalization`;
  canonicalizing the operands does not touch that breadth.
- Rewriting a rollout with two or more substitution sources by applying
  them in sequence (`rolloutSubstitutionSources` orders sources
  longest-first): an earlier rewrite can make a later, not-yet-applied
  source match text it did not match before. `guardSubstitutionOrder`
  catches this by observation: it replays `rewriteRolloutLine` against the
  original line with a growing prefix of the ordered substitution list, so
  each state it checks is what an apply would actually produce, and after
  each step counts whether `rewrite.CountPathInBytesWithJSONEscape` finds
  more occurrences of any later source than before. An increase can only
  come from the step's own output: the replacement can contain the later
  source outright (`oldPath=/real/project`,
  `newPath=/elsewhere/real/project/thing`; `internal/move`'s
  `validateNotNested` blocks only `newPath` nested under `oldPath` from
  the root, not mid-path reappearance), or the replacement plus untouched
  bytes can assemble one (`/longsource` to `/x/foo` inside
  `/longsource/bar` leaves `/x/foo/bar`, completing the unrelated source
  `/foo/bar`). A decrease is always safe; no change is common but not
  guaranteed (see Not covered). Removing the guard reproduces
  self-duplication, suffix completion, and straddling corruption of
  unrelated prose; a single source always succeeds, since one
  `rewrite.ReplacePathInBytes`(`WithJSONEscape`) pass never re-scans its
  own output. `guardSubstitutionOrder` refuses with
  `ErrSubstitutionWouldReintroduceSource`, so plan and apply refuse
  identically rather than previewing a move that later corrupts the
  rollout. An earlier two-pass design (apply the full sequence once, then
  rescan its own output for a second-pass hit) missed suffix-completion
  corruption, which stays stable under a repeated whole-sequence pass;
  checking after each step catches it while reintroduction is still
  observable. A general fix needs a true single-pass multi-pattern
  substitution primitive with JSON-escape awareness in `internal/rewrite`;
  refusing is the narrower answer until it exists.

**Not covered.**

- A recorded cwd whose target no longer exists on disk: a symlink-aliased
  cwd for a since-deleted project falls back to `filepath.Clean` and
  compares lexically (see Handled: `canonicalizePath`), narrower than
  Codex's own `paths_match_after_normalization`.
- Cancellation on every call path whose entry point carries no context,
  since the interface it implements declares no `context.Context`
  parameter. Two chains reach `matchingThreadCWDs` via
  `context.Background()`: `MoveSurfaces` → `projectKnown` →
  `stateDBKnowsProject` → `stateDBFileKnowsProject` (statedb.go), and
  `Placeholders` → `knowsProject` → `countThreadRows` →
  `countMatchingThreadRows` (export_import_stats.go). Neither is
  cancellable mid-scan or bounded, so a scan over a corrupt or hostile own
  state database runs to completion.
- Occurrence counts, not identity (byte positions): a step that introduces
  one new match for a later source while consuming an existing occurrence
  of that source elsewhere in the line nets to no change, so
  `guardSubstitutionOrder` permits it and the later substitution rewrites
  the freshly produced text. Reaching this needs a rollout with two or
  more aliased cwd values, paired with a destination whose replacement
  text both reassembles one recorded source and overlaps an existing
  occurrence of another. Closing it exactly needs tracking occurrence
  identity instead of counts; cc-port accepts the narrower, count-based
  check until that exists. The imprecision runs both ways: a step whose
  introduced match would, once the later source's own substitution ran,
  produce byte-identical text still gets refused, a false refusal rather
  than a real hazard, since a plain count cannot tell the two apart.
- `automations.cwds` stays on `sqlrewrite.CountTextColumnRO`'s literal
  substring scan, not `matchingColumnValues`' canonical comparison: it is
  free-text and multi-value (more than one cwd per row), so the
  single-value matcher does not apply, and detecting an aliased value
  inside would need parsing the field's structure first. A symlink-aliased
  path recorded only in `automations.cwds` therefore produces no warning,
  and `codex-dev.db` is neither rewritten nor used to refuse the move on
  that account. The two single-value columns, `automation_runs.source_cwd`
  and `local_thread_catalog.cwd`, are canonically matched (see Handled
  above).

### Reference thread-ID union

**Handled.**

- `Export` and `ReferenceSurfaces` both need the same thread-ID set for one
  project: its state-database rows (`projectThreadIDs`) unioned with the IDs
  its own rollout files carry. `projectThreadIDSet` computes that union once
  and both callers feed it, so a thread with a state-db row but no matching
  rollout file (or the reverse) counts consistently everywhere instead of
  only where a given call site happened to look.

**Refused.**

- Deriving a project's thread-ID set from rollouts alone anywhere in this
  package. A rollout-only set undercounts relative to `countThreadRows` and
  to what `Export` archives.

**Not covered.**

- Nothing beyond the two current callers; a third caller needing the same
  set uses `projectThreadIDSet` rather than re-deriving it.

### Config never ported

**Handled.**

- There is no `config` export category (`categories` declares only
  `sessions` and `history`). `config.toml` and `<profile>.config.toml` are
  never staged by `Stage` and never written by export; the byte-identical
  guarantee is a round-trip test, not a runtime check, because there is
  simply no import code path that touches the file's content.
- Move still rewrites `config.toml`/`*.config.toml` project keys and values
  in place via `rewrite.TOMLPathRewrite` (`toml.go`), because a move renames
  the live machine's own trust decisions to match the renamed project; that
  is a distinct concern from export/import, which would carry trust across
  machines.
- A move also relocates any project-local `hooks.state` trust key whose hook
  source lived under the moved project: `TOMLPathRewrite` rewrites the key's
  path prefix so the entry stays addressable under the new path. Codex hashes
  hook trust over the hook's command identity, not its source path, so the
  relocated entry's `trusted_hash` still matches and the move relocates trust
  the user already granted rather than porting or re-establishing it. This is
  a same-machine relocation, distinct from the never-ported cross-machine
  decision below.

**Refused.**

- Any export or import surface for `config.toml`. Trust is a per-machine
  decision cc-port does not port; a re-import can never overwrite trust the
  user has re-established on the destination machine.

**Not covered.**

- Nothing: this is a hard, unconditional exclusion with no partial or
  opt-in path.

### Codex-dev refusal semantics

**Handled.**

- `sqlite/codex-dev.db` is a separate development database this adapter does
  not rewrite. Before a move proceeds, `codexDevWarning` inspects its
  `automations.cwds`, `automation_runs.source_cwd`, and
  `local_thread_catalog.cwd` columns for the project path (schema drift in
  any of them also triggers refusal, named in the warning) and, if any
  reference exists, the move refuses via a dedicated `codex-dev-preflight`
  surface whose `Apply` always errors with the warning text.

**Refused.**

- A move whose `codex-dev.db` contains references to the moved project, or
  whose schema no longer matches the three columns this adapter depends on:
  refused before any other surface applies, since a database this package
  cannot safely rewrite would otherwise silently drift from the renamed
  project.

**Not covered.**

- Rewriting `codex-dev.db` itself. It is out of scope entirely; the only
  contract is detect-and-refuse.

### Git baseline handling

Implements this adapter's instance of `docs/architecture.md` §Git-repo-in-state policy (cross-cutting) for `$CODEX_HOME/memories/.git`.

**Handled.**

- `moveGitBaselineToBackup` renames `memories/.git` to a sibling rollback
  backup only when
  `hasNoRemoteGitBaseline` confirms the shape probe (`memories/.git/config`
  exists and contains no `[remote` section), then rewrites the worktree.
  Codex's own baseline helper unconditionally re-initializes a missing or
  unusable `.git`, so removing a no-remote baseline after commit is safe.
- The baseline is staged to a sibling backup during apply and
  removed only once the surrounding move's databases have committed
  (`pendingMoveDatabases.commitSurface`), so an in-process failure can still
  restore it via the registered `Restorer` undo.
- Before every memories worktree apply, `reconcileStrandedGitBackup` removes a
  leftover sibling backup from a prior crashed run, including when the current
  worktree has no path occurrence to rewrite.

**Refused.**

- Deleting `memories/.git` when it carries a `[remote` section. The worktree
  is still rewritten; the git repository state (commits, remotes, refs) is
  left untouched and `memoriesGitBaselineWarning` reports it.

**Not covered.**

- A backup cleanup failure after a successful commit. The commit surface keeps
  the move successful and `gitBackupWarning` reports that residual path.

## Quirks

- `agent_jobs`' `input_csv_path`/`output_csv_path` columns and
  `stage1_outputs`' `raw_memory`/`rollout_summary` columns are free-text
  prose, not path-shaped columns, so they route through
  `sqlrewrite.RewriteTextColumn` (boundary-aware byte rewrite per row).
  `threads.cwd` is matched and rewritten differently: see §cwd matching.
- Move commits the memories and state databases as two separate serial
  transactions, not one joint transaction, because SQLite cannot commit two
  databases atomically, an accepted deviation from spec §6.3 (see
  `databaseapply.go:commitSurface`).
- `stage1_outputs.rollout_slug` is deliberately never rewritten: it is an
  algorithmically derived filename slug (thread id, timestamp, hash), never
  the raw project path, so a path-boundary rewrite would never match it and
  scanning it would be wasted work.
- `~/.agents/plugins/marketplace.json`'s `source` field is the one shared-home
  artifact this adapter rewrites; the populated shape of `~/.agents`
  otherwise is unverified on the development machine (the directory does not
  exist there), so every other path hit under it surfaces only as a residual
  warning, never a rewrite. Exactly one adapter owns this shared path until a
  second consumer of `~/.agents` exists.
- `memories/.git` worktree files are rewritten, while its metadata directory is
  renamed to a rollback backup only behind the shape probe in
  `docs/architecture.md` §Git-repo-in-state policy (cross-cutting).
  `hasNoRemoteGitBaseline` is this adapter's implementation of that probe; see
  §Git baseline handling for the full contract.

## Tests

Unit tests across `move_test.go`, `witness_test.go`, `process_test.go`,
`home_test.go`, `rollout_test.go`, and `export_import_stats_test.go`. Coverage: three-tier `sqlite_home` resolution,
glob-based discovery against generation-suffixed fixture filenames, both
rollout roots, era-A skip behavior under plain and `--deep` rewrite, the
process-table and busy-probe witness sources driven through the injected
process lister rather than the live process table, `codex-dev.db` refusal on both a
path-reference hit and a schema-drift case, the sidecar's apply-and-remainder
counting, `config.toml` byte-identity across an import, a divergent profile
overlay's `sqlite_home` warning, `discoverRolloutFiles` suppressing a
crash-window `.jsonl.zst` sibling, same-second history entries surviving on
distinct `text`, `ReferenceSurfaces` counting a state-database-only
thread the same way `Export` would, `pathMatchesProject` matching a
symlink-aliased cwd against a real symlink built under `t.TempDir`, and a
symlink-aliased thread row's dry-run count agreeing with what move
actually rewrites.

Fixtures come from `testdata/dotcodex/` staged via `SetupFixture`, following
the `testutil.SetupFixture` pattern: `SetupFixture` copies the static tree
and then builds `state_5.sqlite`, `memories_1.sqlite`, and the
`memories/.git` no-remote baseline at test runtime, because SQLite files are
binary and a nested `.git` directory is untrackable by the outer repository.
All fixture content (project paths, thread IDs) is synthetic; nothing is
copied from a real `~/.codex`.
