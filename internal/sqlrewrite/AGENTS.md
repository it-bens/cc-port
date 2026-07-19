# internal/sqlrewrite: agent notes

## Before editing

- Set `busy_timeout=0` on every connection this package opens; never wait on a busy database. (README §Busy handling)
- Never use `LIKE` in a path predicate. Use the exact/prefix `substr` form. (README §LIKE ban)
- Checkpoint on `Open` before the version check, and again after a rewrite transaction commits. (README §Checkpoint discipline)
- Validate schema (columns, primary key) before any query; fail with the observed schema in the error. (README §Byte-exact predicates and schema validation)
- Keep the SQLite version floor check and its drift test in sync with Codex's own pin. (README §SQLite version floor)

## Navigation

- Entry: `sqlrewrite.go:Open`.
- Path predicates: `sqlrewrite.go:CountPathColumnRO`, `sqlrewrite.go:RewritePathColumn`.
- Text/blob rewrite: `sqlrewrite.go:RewriteTextColumn`.
- Update-only mutation: `sqlrewrite.go:UpdateColumnsByKey`.
- Tests: `sqlrewrite_test.go`.
