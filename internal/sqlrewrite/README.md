# internal/sqlrewrite

## Purpose

SQL-level path rewriting on SQLite. Byte-level substring replacement corrupts
SQLite pages, so every database mutation a tool adapter performs on user
paths goes through SQL on a `modernc.org/sqlite` connection this package
opens and guards.

## Public API

- `DB`, `Open(path string) (*DB, error)`: opens `path` (through `FileDSN`)
  with a zero busy timeout and folds its WAL into the main database before
  any caller can observe its contents.
- `FileDSN(path string, params map[string]string) string`: encodes `path` as
  a `file:` URL DSN carrying `params` as its query string, so a `?` or other
  DSN-significant byte in `path` still addresses the intended file. Query
  keys are sorted, so the same `params` always produce the same DSN.
- `(*DB).Close() error`
- `(*DB).Begin() (*Tx, error)`, `Tx`, `(*Tx).Commit() error`, `(*Tx).Rollback() error`.
- `(*DB).CheckpointTruncate() error`: folds the WAL into the main database
  and truncates it. Called once more after a rewrite transaction commits.
  - `CountTextColumnRO(db *sql.DB, table, column, oldPath string) (int, error)`:
    counts TEXT or BLOB rows containing a boundary-aware reference to `oldPath`
    on the caller's read-only connection.
- `(*DB).RewriteTextColumn(tx *Tx, table, primaryKeyColumn, column, oldPath, newPath string) (int, error)`:
  streams matching TEXT or BLOB rows, applies `rewrite.ReplacePathInBytes` in
  Go, and writes each changed row back by its declared primary key.
- `RequirePrimaryKeyAndColumns(db *sql.DB, table, primaryKeyColumn string, columns ...string) error`:
  the schema check `UpdateColumnsByKey` runs, on the caller's read-only
  connection, so a read-only plan refuses the same schema its apply would.
- `(*DB).UpdateColumnsByKey(tx *Tx, table, primaryKeyColumn string, primaryKey any, values, expected map[string]any) (int, error)`:
  updates columns on an existing row identified by its single-column primary
  key, only while each `expected` column still holds its expected value; a
  nil or empty `expected` adds no condition. Never inserts.
- `(*DB).UpdateColumnsByRowID(tx *Tx, table string, rowID int64, values, expected map[string]any) (int, error)`:
  updates columns on an existing row identified by its SQLite rowid, for a
  table whose declared primary key is composite, only while each `expected`
  column still holds its expected value. `expected` must name at least one
  column. Never inserts.
- `RequireRowIDTableAndColumns(db *sql.DB, table string, columns ...string) error`:
  the schema check `UpdateColumnsByRowID` runs, on the caller's read-only
  connection, so a read-only plan refuses the same schema its apply would.

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

### DSN construction

**Handled.**

- `FileDSN` builds a `file:` URL through `net/url`, so a `?` inside `path`
  is percent-encoded as part of the URL's path component rather than left as
  a literal byte the DSN parser could mistake for the query separator.
  `Open` calls `FileDSN(path, nil)`; `internal/tool/codex`'s
  `openReadOnlyDatabase` calls `FileDSN(path, map[string]string{"mode": "ro"})`;
  its `probeDatabaseBusy` calls `FileDSN(path, nil)` and passes the result to
  `sql.Open` directly, since it needs a write connection this package's
  `Open` would checkpoint. `TestFileDSNEncodesQuestionMarkPath` round-trips a
  table through a path whose directory segment contains `?`.
- Query keys are sorted (`url.Values.Encode`), so two `FileDSN` calls with
  the same `params` produce byte-identical DSNs.

**Refused.**

- Handing a raw, non-`file:`-prefixed path straight to `sql.Open`. The
  `modernc.org/sqlite` driver truncates such a DSN at the first `?` and
  reinterprets everything after it as query parameters, so a path
  containing `?` silently opens a different file than the one named. Every
  connection this package or `internal/tool/codex` opens goes through
  `FileDSN` instead.

**Not covered.**

- A path byte the underlying filesystem itself rejects. `FileDSN` assumes
  the OS accepts whatever `path` names; it only protects the DSN parser.

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

### Byte-exact predicates and schema validation

**Handled.**

- `CountTextColumnRO`, `RewriteTextColumn`, the two keyed updates
  (`UpdateColumnsByKey`, `UpdateColumnsByRowID`), and
  `RequirePrimaryKeyAndColumns` call `PRAGMA table_info` first and refuse with the
  observed schema in the error message when a declared column or primary key
  is missing, so a schema surprise fails loudly naming what was actually
  found rather than producing a confusing SQL error deeper in the call.
  `Open`, `Begin`, `Commit`, `Rollback`, and `CheckpointTruncate` do not
  validate schema; they operate on the connection or transaction itself, not
  a named table.
- `RewriteTextColumn` and `UpdateColumnsByKey` additionally require the
  declared primary key column to actually be the table's primary key and
  refuse a composite primary key, since both operations key their per-row
  update on a single column. `CountTextColumnRO` has no per-row update to
  key, so it validates the column alone and does not take a primary key
  parameter.
- `UpdateColumnsByRowID` keys its update on the rowid and requires an
  ordinary rowid table. It reads the table's `type` and `wr` flag from
  `pragma_table_list`, which SQLite fills from the parsed schema, and
  requires exactly one row of type `table` with `wr = 0`. Matching
  `WITHOUT ROWID` in the stored `CREATE` text would depend on how that text
  is written, and a `SELECT rowid` probe succeeds on a `WITHOUT ROWID`
  table that declares a column named `rowid`. For the same reason it
  refuses a table declaring a column named `rowid` in any letter case: such
  a column shadows the real rowid in `WHERE rowid = ?`. A column named `oid`
  or `_rowid_` shadows only its own name, so the update still addresses the
  real rowid. `TestUpdateColumnsByRowIDRefusesUnsupportedSchema` covers a
  missing column, a `WITHOUT ROWID` table, a `RowID` column, a view, and an
  `fts5` virtual table;
  `TestUpdateColumnsByRowIDAddressesRealRowIDBesideOidAndUnderscoreRowIDColumns`
  covers the accepted aliases.
- The keyed updates' `expected` map adds one `"<column>" COLLATE BINARY = ?`
  predicate per entry, so the update writes only a row whose guarded columns
  still hold the expected bytes, even in a column declared with a
  case-insensitive collation. A row that no longer matches is left unchanged
  and the call returns zero; the caller decides whether zero is an error.
  Every `expected` column goes through the same column check as the written
  columns. `UpdateColumnsByRowID` requires a non-empty `expected`, because
  SQLite can hand a freed rowid to a different row; a declared primary key
  stays with its row, so `UpdateColumnsByKey` accepts none.
  `TestUpdateColumnsByRowIDWritesOnlyWhileExpectedValueHolds` covers an
  unchanged value, a changed value, and a value changed only in letter case;
  `TestUpdateColumnsByKeyWritesOnlyWhileExpectedValueHolds` covers an
  unchanged and a changed value.
- `RewriteTextColumn` and `CountTextColumnRO` each read a column's runtime
  value as `any` and type switch on `string` vs. `[]byte`, so the same
  column reads correctly whether it is declared TEXT or BLOB, without the
  caller stating which. `TestRewriteTextColumnPreservesTextAndBlobStorage`
  asserts a BLOB column's binary bytes (including `0x00`) survive the
  rewrite unchanged outside the rewritten path substring.

**Refused.**

- `RewriteTextColumn` or `CountTextColumnRO` against a column of any type
  other than `string` or `[]byte` (TEXT/BLOB): a hard error naming the
  observed Go type. This check applies only to the two operations that read
  a row's value back into Go; the keyed updates write caller-supplied
  values straight through as SQL parameters and do not type-check them.
- Either of the two read-or-rewrite operations above, or either keyed
  update, against a table or column the schema query does not find, or (for
  `RewriteTextColumn`/`UpdateColumnsByKey`) a primary key column that either
  does not exist or is not actually the table's primary key.
- `UpdateColumnsByRowID` against a `WITHOUT ROWID` table, a view, a virtual
  table, a name matching more than one schema, or a table declaring a column
  named `rowid`.
- A nil value in either keyed update's `expected` map, untyped or a typed
  nil such as a nil pointer: either binds SQL `NULL`, and `= NULL` never
  matches, so the call refuses it instead of silently updating nothing.
- `UpdateColumnsByRowID` with a nil or empty `expected` map.
  `TestUpdateColumnsByRowIDRefusesMissingExpectedValue` covers a nil map, an
  empty map, a nil value, and a nil `*string`.

**Not covered.**

- Schema migration. This package validates the schema it finds; it does not
  alter a table's structure.
- Validating the Go type of a value passed to a keyed update. The
  caller is responsible for passing a value the underlying column accepts;
  a wrong type surfaces as whatever error `database/sql` itself returns.

### Update-only mutation

**Handled.**

- `UpdateColumnsByKey` and `UpdateColumnsByRowID` never insert a row.
  `TestUpdateColumnsByKeyUpdatesExistingRowWithoutInsert` and
  `TestUpdateColumnsByRowIDReportsZeroForAbsentRowWithoutInsert` assert a
  call against a missing key updates zero rows, reports zero, and leaves the
  table's row count unchanged. `internal/tool/codex` uses `UpdateColumnsByKey`
  to apply the threads sidecar (see `internal/tool/codex/README.md`
  §Sidecar update-only rationale), and both to rewrite the state database's
  project-path columns on move (see `internal/tool/codex/README.md` §cwd
  matching): the state database is a foreign, self-healing derived cache to
  that caller, and an `INSERT` into it would fight Codex's own reconciler.

**Refused.**

- None at the SQL layer; "no insert" is enforced by the query shape
  (`UPDATE ... WHERE <primary key> = ?` or `UPDATE ... WHERE rowid = ?`,
  plus any expected-value conditions), which structurally cannot create a
  row.

**Not covered.**

- Reconstitution inserts. A future adapter reconstituting its own primary SQL
  store (not a foreign cache) performs `INSERT`s through its own queries on a
  connection this package's `Open` still opens; that is expected new adapter
  work, not a gap in this package.

## Tests

Unit tests in `sqlrewrite_test.go`: the version-floor drift test, the
busy-refusal timing test, the checkpoint-on-open test against a fixture
database with a synthetic `-wal`, `FileDSN` round-tripping a table through a
path whose directory segment contains `?`, `RewriteTextColumn` fixtures
covering a TEXT and a BLOB column, the update-without-insert behavior of
`UpdateColumnsByKey` and `UpdateColumnsByRowID`, `UpdateColumnsByRowID`
updating one row of a composite-key table by rowid, both expected-value
guards, `UpdateColumnsByRowID`'s refusal of a missing expected value, and
its schema refusals.
