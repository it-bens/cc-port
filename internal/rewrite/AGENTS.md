# internal/rewrite — agent notes

## Before editing

- Do not widen the path-boundary check beyond `[A-Za-z0-9_-]` without updating the dot-lookahead (README §Boundary rules).
- Do not collapse the `.` lookahead to a single-byte check (README §Boundary rules).
- Do not widen `FindPlaceholderTokens` beyond the `{{[A-Z0-9_]{1,64}}}` grammar (README §Placeholder scanning).

## Navigation

- Entry: `rewrite.go:ReplacePathInBytes`.
- Promoter: `rewrite.go:SafeRenamePromoter`, `rewrite.go:NewSafeRenamePromoter`.
- Scanner: `rewrite.go:FindPlaceholderTokens`.
- Tests: `rewrite_test.go`, `rewrite_fuzz_test.go`.
