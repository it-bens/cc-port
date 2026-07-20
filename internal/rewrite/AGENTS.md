# internal/rewrite: agent notes

## Before editing

- Do not widen the path-boundary check beyond `[A-Za-z0-9_-]` without updating the dot-lookahead (README §Boundary rules).
- Do not collapse the `.` lookahead to a single-byte check (README §Boundary rules).
- Never let `TOMLPathRewrite` skip its key-path-multiset validation or its quote/backslash refusal, even for a caller that claims a trusted input. (README §TOML boundary rules)

## Navigation

- Entry: `rewrite.go:ReplacePathInBytes`.
- TOML rewrite: `rewrite.go:TOMLPathRewrite`.
- Promoter: `rewrite.go:SafeRenamePromoter`, `rewrite.go:NewSafeRenamePromoter`.
- Directory promotion: `directory_promoter.go` (`PromoteDir`, `IsArtifactPath`).
- Tests: `rewrite_test.go`, `rewrite_fuzz_test.go`, `toml_test.go`.
