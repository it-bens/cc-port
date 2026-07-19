# internal/tool/codex

## Purpose

Implements `tool.Tool` and `tool.Workspace` for OpenAI Codex. Codex stores
project-associated state in shapes Claude Code never uses: verbatim absolute
`cwd` strings, a WAL-mode SQLite index with a live desktop writer, TOML
tables keyed by project path, optionally zstd-compressed session files, and a
git-baselined memory directory. This adapter concentrates every one of those
tool-specific facts in this package; `internal/move`, `internal/export`,
`internal/importer`, and `internal/stats` know nothing about Codex.

## Public API

- `Adapter`, `New() *Adapter`: wired to the real environment, process table,
  and wall clock. `NewAdapter(getenv, listProcesses, now, transcodeCaps) *Adapter`:
  same shape with every seam explicit, for tests.
- `Home`: `Dir`, `SQLiteDir`, `AgentsDir`.
- `Workspace`, `NewWorkspace(home, getenv, listProcesses, now, transcodeCaps) *Workspace`,
  `NewWorkspaceForTest(home, getenv, listProcesses, now, pidAlive, transcodeCaps) *Workspace`:
  the test variant additionally overrides the process-liveness check so
  adapter tests never touch the live process table.
- `ProcessLister func() ([]ProcessInfo, error)`, `ProcessInfo{PID, Name}`: the
  process-enumeration seam; production default is `listSystemProcesses`
  (shells out to `ps -Ao pid=,comm=`), darwin/linux only.
- `TranscodeCaps{MaxDecompressedBytes, MaxLineBytes}`: zstd decompression caps.
- `TranscodeLines(path, caps, transform)`: rewrites a rollout file (plain
  `.jsonl` or its `.jsonl.zst` sibling) line by line, decompressing/recompressing
  transparently, promoted through
  `rewrite.SafeWriteFile`.
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

**Refused.**

- An explicit `--codex-home` that does not exist, is not a directory, or
  cannot resolve through `filepath.EvalSymlinks`: `Open` returns an error
  before constructing a `Workspace`.

**Not covered.**

- A `sqlite_home` value that itself does not exist. Resolution only computes
  the path; database discovery (`discoverDatabases`) separately treats a
  missing directory as "no databases found," not an error.

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
  (`thread-store/src/local/archive_thread.rs:41-53`). `rolloutRoots` and
  `discoverRolloutFiles` walk both roots every time, so every rollout
  surface (move rewrite, export, the witness's freshness check, residual
  scanning) sees the same combined file set regardless of which root a given
  rollout currently sits under.
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

- `ActiveWriters` gathers five evidence sources, every one regardless of an
  earlier source's outcome, so a dry-run reports every signal at once: (1) a
  running Codex process (`codex`, `codex-tui`, `codex-app-server`) is the
  primary signal, since a plain `codex`/`codex exec` run holds a database
  open with no daemon directory and no marker file; (2) any rollout under
  either root, or any `-wal`/`-shm` sibling, modified within the last 120
  seconds; (3) a live PID in `app-server-daemon/app-server.pid`, or a held
  flock on `app-server-daemon/daemon.lock`; (4)
  `$CODEX_HOME/.tmp/rollout-compression.lock`, counted only when its mtime is
  inside a six-hour staleness window and its embedded PID is alive, since the
  marker persists after a successful run and presence alone proves nothing;
  (5) `SQLITE_BUSY` on a `BEGIN IMMEDIATE` probe against each discovered
  database.
- A source that cannot be read (not merely finds nothing) makes the whole
  call return an error wrapping `tool.ErrNoWitness`, which blocks mutation
  exactly like a positive result: an unreadable witness cannot be treated as
  "no writers."

**Refused.**

- Trusting presence of the compression lock marker alone. Its age and
  embedded PID are both checked; a stale or dead-PID marker is not evidence.

**Not covered.**

- A cooperative shutdown protocol. The desktop app offers none; detection is
  best-effort evidence with hard refusal on any positive signal, backstopped
  by `sqlrewrite`'s `busy_timeout=0` at actual write time. The witness also
  does not cross-check a PID file's embedded process start time against the
  live process's actual start time, a deliberate scope simplification
  against Codex's own PID-reuse guard.

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
  timestamp)`, and `appendUniqueExact` deduplicates by exact line match. Both
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
  `sqlrewrite.RewriteTextColumn` (boundary-aware byte rewrite per row) rather
  than `RewritePathColumn`'s exact/prefix SQL predicate. `threads.cwd` is the
  one column that gets the exact/prefix predicate and its read-only count uses
  `sqlrewrite.CountPathColumnRO`, so both consume `internal/sqlrewrite`'s
  single predicate definition. Codex stores it
  as a verbatim canonicalized path with no free text around it, an accepted
  deviation from spec §6.3.
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
`home_test.go`, `rollout_test.go`, `transcode_test.go`, and
`export_import_stats_test.go`. Coverage: three-tier `sqlite_home` resolution,
glob-based discovery against generation-suffixed fixture filenames, both
rollout roots, era-A skip behavior under plain and `--deep` rewrite, the
witness's five evidence sources driven through the injected process lister
and clock (fake PID files, fresh and stale marker mtimes) rather than the
live process table or wall clock, `codex-dev.db` refusal on both a
path-reference hit and a schema-drift case, the sidecar's apply-and-remainder
counting, and `config.toml` byte-identity across an import.

`transcode_large_test.go` (`-tags large`) exercises the zstd decompression
caps at production scale; the default suite asserts the per-line cap wins
when one compressed line also exceeds the aggregate cap, per the pairing pattern in
`internal/archive/README.md`.

Fixtures come from `testdata/dotcodex/` staged via `SetupFixture`, following
the `testutil.SetupFixture` pattern: `SetupFixture` copies the static tree
and then builds `state_5.sqlite`, `memories_1.sqlite`, and the
`memories/.git` no-remote baseline at test runtime, because SQLite files are
binary and a nested `.git` directory is untrackable by the outer repository.
All fixture content (project paths, thread IDs) is synthetic; nothing is
copied from a real `~/.codex`.
