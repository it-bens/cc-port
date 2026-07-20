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
- `CountPathColumnRO(db *sql.DB, table, column, oldPath string) (int, error)`:
  counts values equal to `oldPath` or nested below it at a `/` boundary on the
  caller's read-only connection.
- `(*DB).RewritePathColumn(tx *Tx, table, column, oldPath, newPath string) (int, error)`:
  rewrites exact and slash-boundary-prefixed path values.
- `CountTextColumnRO(db *sql.DB, table, column, oldPath string) (int, error)`:
  counts TEXT or BLOB rows containing a bounded reference to `oldPath` on the
  caller's read-only connection, sharing `RewriteTextColumn`'s per-value byte
  cap and refusal.
- `(*DB).RewriteTextColumn(tx *Tx, table, primaryKeyColumn, column, oldPath, newPath string) (int, error)`:
  streams matching TEXT or BLOB rows, applies `rewrite.ReplacePathInBytes` in
  Go, and writes each changed row back by its declared primary key.
- `ErrTextValueTooLarge`: the sentinel `RewriteTextColumn` and
  `CountTextColumnRO` both wrap when a candidate value exceeds the shared
  byte cap; callers assert against it with `errors.Is`.
- `MaxTextValueBytes`: the shared per-value byte cap (16 MiB)
  `RewriteTextColumn` and `CountTextColumnRO` enforce, exported so a caller
  guarding its own path-value materialization outside this package's
  predicates (`internal/tool/codex`'s `threads.cwd` matcher) can reuse the
  same cap instead of maintaining a second one.
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

### LIKE ban

**Handled.**

- The path-column predicate is defined once and drives both
  `CountPathColumnRO` and `RewritePathColumn`: `col COLLATE BINARY = :old OR
  substr(col, 1, length(:old)+1) COLLATE BINARY = :old || '/'`. `_` and `%` are SQL
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

- Nothing outside this package. `CountTextColumnRO` and `RewriteTextColumn`
  both use `instr()`, not `LIKE`, for free-text columns like `agent_jobs`'s
  CSV paths, and both live here so the count and the rewrite share one
  predicate and one schema check.

### Byte-exact predicates and schema validation

**Handled.**

- `CountPathColumnRO`, `RewritePathColumn`, `CountTextColumnRO`,
  `RewriteTextColumn`, and the generic keyed update (`UpdateColumnsByKey`)
  call `PRAGMA table_info` first and refuse with the observed schema in the
  error message when a declared column or primary key is missing, so a
  schema surprise fails loudly naming what was actually found rather than
  producing a confusing SQL error deeper in the call. `Open`, `Begin`,
  `Commit`, `Rollback`, and `CheckpointTruncate` do not validate schema; they
  operate on the connection or transaction itself, not a named table.
- `RewriteTextColumn` and `UpdateColumnsByKey` additionally require the
  declared primary key column to actually be the table's primary key and
  refuse a composite primary key, since both operations key their per-row
  update on a single column. `CountTextColumnRO` has no per-row update to
  key, so it validates the column alone and does not take a primary key
  parameter.
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
  a row's value back into Go; `UpdateColumnsByKey` writes caller-supplied
  values straight through as SQL parameters and does not type-check them.
- Any of the four read-or-rewrite operations above, or `UpdateColumnsByKey`,
  against a table or column the schema query does not find, or (for
  `RewriteTextColumn`/`UpdateColumnsByKey`) a primary key column that either
  does not exist or is not actually the table's primary key.

**Not covered.**

- Schema migration. This package validates the schema it finds; it does not
  alter a table's structure.
- Validating the Go type of a value passed to `UpdateColumnsByKey`. The
  caller is responsible for passing a value the underlying column accepts;
  a wrong type surfaces as whatever error `database/sql` itself returns.

### Bounded value materialization

**Handled.**

- `RewriteTextColumn` and `CountTextColumnRO` share one `MaxTextValueBytes`
  cap (16 MiB) and one `octet_length(...) > ?` guard shape, so a candidate
  value too large to materialize safely is refused identically whether the
  caller is planning a move (read-only) or applying one (transactional).
  Both wrap `ErrTextValueTooLarge`, so a caller checks the refusal with
  `errors.Is` regardless of which function produced it.
  `TestRewriteTextColumnRefusesOversizedValues` and
  `TestCountTextColumnRORefusesOversizedValue` each insert a value one byte
  over the cap and assert the refusal through that sentinel. `MaxTextValueBytes`
  is exported so a caller matching path values outside a single-column
  predicate (`internal/tool/codex`'s canonicalizing `threads.cwd` matcher,
  see its README §cwd matching) can guard against the same class of
  oversized value with the same cap.

**Refused.**

- Reading an oversized value's full bytes before checking its size. Both
  functions run the `octet_length` guard as its own query first, so the
  oversized row's TEXT/BLOB payload is never pulled into Go memory.

**Not covered.**

- A cap tighter than 16 MiB for a specific table or column. The cap is
  package-wide; no call site can lower it.

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
database with a synthetic `-wal`, `FileDSN` round-tripping a table through a
path whose directory segment contains `?`, `RewriteTextColumn` fixtures
covering a TEXT and a BLOB column, the shared `ErrTextValueTooLarge` refusal
asserted from both `RewriteTextColumn` and `CountTextColumnRO`,
`UpdateColumnsByKey`'s update-without-insert behavior, the path-predicate
boundary-parity table shared with `rewrite.ReplacePathInBytes`, and the
schema-validation refusal naming the observed columns.
