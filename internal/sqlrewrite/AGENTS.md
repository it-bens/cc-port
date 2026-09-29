# internal/sqlrewrite: agent notes

## Before editing

- Set `busy_timeout=0` on every connection this package opens; never wait on a busy database. (README §Busy handling)
- Checkpoint on `Open` before the version check, and again after a rewrite transaction commits. (README §Checkpoint discipline)
- Validate schema (columns, primary key) before any query; fail with the observed schema in the error. (README §Byte-exact predicates and schema validation)
- Keep the SQLite version floor check and its drift test in sync with Codex's own pin. (README §SQLite version floor)
- Run a transaction's statements under `context.WithoutCancel(ctx)` and check `ctx.Err()` on entry to each mutator and at the top of every row in `RewriteTextColumn`; leave a statement outside a transaction on the live `ctx`. The per-row check is pinned by `TestRewriteTextColumnReportsCancellationForARowTheBoundaryRuleRejects`, the entry check and the rollback that follows by `TestCancelledRewriteTextColumnLeavesTransactionRollbackable`. (README §Cancellation)

## Navigation

- Entry: `sqlrewrite.go:Open`.
- Text/blob rewrite: `sqlrewrite.go:RewriteTextColumn`.
- Update-only mutation: `sqlrewrite.go:UpdateColumnsByKey`, `sqlrewrite.go:UpdateColumnsByRowID`.
- Tests: `sqlrewrite_test.go`.
