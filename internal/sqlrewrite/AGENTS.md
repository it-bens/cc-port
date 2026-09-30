# internal/sqlrewrite: agent notes

## Before editing

- Open every connection through `open` with the 5 s busy timeout and immediate transactions, both carried in the DSN. (README §Busy handling)
- Checkpoint on `Open` before the version check, and again after a rewrite transaction commits. (README §Checkpoint discipline)
- Validate schema (columns, primary key) before any query; fail with the observed schema in the error. (README §Byte-exact predicates and schema validation)
- Keep the SQLite version floor check and its drift test in sync with Codex's own pin. (README §SQLite version floor)
- Run a transaction's statements under `context.WithoutCancel(ctx)`; `RewriteTextColumn` checks `ctx.Err()` on entry. (README §Cancellation)

## Navigation

- Entry: `sqlrewrite.go:Open`.
- Text/blob rewrite: `sqlrewrite.go:RewriteTextColumn`.
- Update-only mutation: `sqlrewrite.go:UpdateColumnsByKey`, `sqlrewrite.go:UpdateColumnsByRowID`.
- Tests: `sqlrewrite_test.go`.
