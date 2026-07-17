# internal/sqlrewrite

## Purpose

SQL-level path rewriting on SQLite. Byte-level substring replacement corrupts
SQLite pages, so every database mutation a tool adapter performs on user
paths goes through SQL on a `modernc.org/sqlite` connection this package
opens and guards.

## Public API

- `DB`, `Open(path string) (*DB, error)`: opens `path` with a zero busy
  timeout and folds its WAL into the main database before any caller can
  observe its contents.
- `(*DB).Close() error`
- `(*DB).Begin() (*Tx, error)`, `Tx`, `(*Tx).Commit() error`, `(*Tx).Rollback() error`.
- `(*DB).CheckpointTruncate() error`: folds the WAL into the main database
  and truncates it. Called once more after a rewrite transaction commits.
- `(*DB).CountPathColumn(table, column, oldPath string) (int, error)`: counts
  values equal to `oldPath` or nested below it at a `/` boundary.
- `(*DB).RewritePathColumn(tx *Tx, table, column, oldPath, newPath string) (int, error)`:
  rewrites exact and slash-boundary-prefixed path values.
- `(*DB).RewriteTextColumn(tx *Tx, table, primaryKeyColumn, column, oldPath, newPath string) (int, error)`:
  streams matching TEXT or BLOB rows, applies `rewrite.ReplacePathInBytes` in
  Go, and writes each changed row back by its declared primary key.
- `(*DB).UpdateColumnsByKey(tx *Tx, table, primaryKeyColumn string, primaryKey any, values map[string]any) (int, error)`:
  updates columns on an existing row identified by its single-column primary
  key; never inserts.

## Contracts

### SQLite version floor

**Handled.**

- `Open` queries `sqlite_version()` and refuses to proceed below `3.51.3`,
  the version Codex pins for its own WAL-reset corruption fix
  (`codex-rs/state/src/lib.rs:7-10`). `TestBundledSQLiteVersionMeetsRequiredFloor`
  is the drift test: it fails the moment the bundled `modernc.org/sqlite` pin
  drops below the floor, so a routine dependency bump cannot silently
  reintroduce the corruption class Codex itself patched around.

**Refused.**

- A database whose reported version parses to below the floor: `Open`
  returns an error naming both versions before any query runs against the
  connection.

**Not covered.**

- Verifying the *content* of the WAL-reset fix. The floor is a version-number
  gate; it trusts that `modernc.org/sqlite` correctly implements whatever
  SQLite `3.51.3` fixed, the same way Codex's own pin does.

### Busy handling

**Handled.**

- `Open` sets `PRAGMA busy_timeout=0` immediately after opening the
  connection, so any `SQLITE_BUSY` from a concurrent writer returns
  immediately as a hard error rather than blocking. `TestOpenRefusesBusyWriterImmediately`
  asserts the refusal completes in under a second against a held writer
  transaction.
- The connection pool is capped to one connection (`SetMaxOpenConns(1)`,
  `SetMaxIdleConns(1)`), so cc-port's own internal concurrency cannot
  self-contend against the same handle.

**Refused.**

- Waiting on a busy database. A zero busy timeout is a deliberate refusal to
  retry: cc-port's caller (the move apply bracket) needs a definite answer,
  not a stall, when another writer holds the database.

**Not covered.**

- Detecting *which* process holds the busy lock. The busy error reports that
  a writer exists, not its identity; identifying a live Codex process is the
  witness's job (`internal/tool/codex/README.md` §Witness evidence order),
  not this package's.

### Checkpoint discipline

**Handled.**

- `Open` runs `PRAGMA wal_checkpoint(TRUNCATE)` before the version check and
  before returning the `*DB`, so a stale `-wal` left by a crashed writer is
  folded into the main database before anything is observed or rewritten.
  `TestOpenFoldsWALBeforeMainDatabaseIsObserved` verifies a row written only
  to the WAL is visible in the main database file only after `Open` and
  `Close` have run.
- `CheckpointTruncate` runs again after a rewrite transaction commits, so the
  rewritten rows are folded back into the main file rather than left pending
  in the WAL.

**Refused.**

- A checkpoint that reports itself busy: `checkpointTruncate` treats a
  non-zero busy result from `wal_checkpoint(TRUNCATE)` as a hard error naming
  the pending frame counts, rather than silently leaving the WAL unfolded.

**Not covered.**

- Automatic periodic checkpointing during a long-running transaction.
  Checkpoints run only at `Open` and after a rewrite commits.

### LIKE ban

**Handled.**

- Every path predicate in this package uses `col = :old OR substr(col, 1,
  length(:old)+1) = :old || '/'` rather than `LIKE`. `_` and `%` are SQL
  `LIKE` wildcards and ASCII `LIKE` comparison is case-insensitive by
  default, so `LIKE old || '/%'` would match a sibling project whose name
  merely resembles the target: `/a/my_app` would match `/a/myXapp`, and
  nested real-world working directories such as `erstizeitung` and
  `erstizeitung/pwa` turn this from a theoretical risk into a concrete
  prefix-collision bug.
- `TestPathPredicateAgreesWithByteRewriter` is the boundary-parity table:
  underscores, percent signs, nested sibling projects, and case variants, all
  asserting the SQL predicate agrees with `rewrite.ReplacePathInBytes` on the
  same case.

**Refused.**

- `LIKE` anywhere in this package's SQL. There is no override; a new
  predicate follows the exact/prefix form above.

**Not covered.**

- Predicates outside this package. `internal/tool/codex`'s read-only
  `countTextRows` helper (used for free-text columns like `agent_jobs`'s CSV
  paths) uses `instr()`, not `LIKE`, for the same reason; it is a separate
  helper because it does not need this package's transaction or schema
  machinery.

### Byte-exact predicates and schema validation

**Handled.**

- Every path-rewrite operation (`CountPathColumn`, `RewritePathColumn`,
  `RewriteTextColumn`) and the generic keyed update (`UpdateColumnsByKey`)
  call `PRAGMA table_info` first and refuse with the observed schema in the
  error message when a declared column or primary key is missing, so a
  schema surprise fails loudly naming what was actually found rather than
  producing a confusing SQL error deeper in the call. `Open`, `Begin`,
  `Commit`, `Rollback`, and `CheckpointTruncate` do not validate schema; they
  operate on the connection or transaction itself, not a named table.
- `RewriteTextColumn` and `UpdateColumnsByKey` additionally require the
  declared primary key column to actually be the table's primary key and
  refuse a composite primary key, since both operations key their per-row
  update on a single column.
- `RewriteTextColumn` reads a column's runtime value as `any` and type
  switches on `string` vs. `[]byte`, so the same code path handles a TEXT
  column and a BLOB column without the caller declaring which one it is.
  `TestRewriteTextColumnPreservesTextAndBlobStorage` asserts a BLOB column's
  binary bytes (including `0x00`) survive the rewrite unchanged outside the
  rewritten path substring.

**Refused.**

- `RewriteTextColumn` against a column of any type other than `string` or
  `[]byte` (TEXT/BLOB): a hard error naming the observed Go type. This check
  is `RewriteTextColumn`-only, since it is the one operation that reads a
  row's value back into Go before writing it; `UpdateColumnsByKey` writes
  caller-supplied values straight through as SQL parameters and does not
  type-check them.
- Any of the three path-rewrite operations, or `UpdateColumnsByKey`, against
  a table or column the schema query does not find, or (for
  `RewriteTextColumn`/`UpdateColumnsByKey`) a primary key column that either
  does not exist or is not actually the table's primary key.

**Not covered.**

- Schema migration. This package validates the schema it finds; it does not
  alter a table's structure.
- Validating the Go type of a value passed to `UpdateColumnsByKey`. The
  caller is responsible for passing a value the underlying column accepts;
  a wrong type surfaces as whatever error `database/sql` itself returns.

### Update-only mutation

**Handled.**

- `UpdateColumnsByKey` never inserts a row. `TestUpdateColumnsByKeyUpdatesExistingRowWithoutInsert`
  asserts a call against a missing primary key updates zero rows and leaves
  the table's row count unchanged. This is the primitive `internal/tool/codex`
  uses to apply the threads sidecar (see `internal/tool/codex/README.md`
  §Sidecar update-only rationale): the state database is a foreign,
  self-healing derived cache to that caller, and an `INSERT` into it would
  fight Codex's own reconciler.

**Refused.**

- None at the SQL layer; "no insert" is enforced by the query shape
  (`UPDATE ... WHERE <primary key> = ?`), which structurally cannot create a
  row.

**Not covered.**

- Reconstitution inserts. A future adapter reconstituting its own primary SQL
  store (not a foreign cache) performs `INSERT`s through its own queries on a
  connection this package's `Open` still opens; that is expected new adapter
  work, not a gap in this package.

## Tests

Unit tests in `sqlrewrite_test.go`: the version-floor drift test, the
busy-refusal timing test, the checkpoint-on-open test against a fixture
database with a synthetic `-wal`, `RewriteTextColumn` fixtures covering a
TEXT and a BLOB column, `UpdateColumnsByKey`'s update-without-insert
behavior, the path-predicate boundary-parity table shared with
`rewrite.ReplacePathInBytes`, and the schema-validation refusal naming the
observed columns.
