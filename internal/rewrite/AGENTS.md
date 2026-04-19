# internal/rewrite — agent notes

Byte-level path rewrite primitives and the atomic-rename promoter. See `README.md` for the full contract.

## Before editing

- Do not widen the path-boundary check to accept bytes beyond `[A-Za-z0-9_-]` without updating the dot-lookahead reasoning — both sides of the heuristic move together (README §Boundary rules).
- The `.` lookahead walks the full run of consecutive dots before inspecting the first non-dot byte; do not collapse it to a single-byte check — that would break either extension suppression (`/a/foo.v2`) or sentence-terminating prose (`see /a/foo.`, `see /a/foo... done`) (README §Boundary rules).
- `FindPlaceholderTokens` is the tamper-defense scan — its grammar is intentionally narrow to `{{UPPER_SNAKE}}`; widening it produces false positives on legitimate `{{…}}` content in transcripts (README §Quirks §Placeholder-token grammar is narrow by design).

## Navigation

- Entry: `rewrite.go:ReplacePathInBytes`.
- Promoter: `rewrite.go:SafeRenamePromoter`, `rewrite.go:NewSafeRenamePromoter`.
- Scanners: `rewrite.go:FindPlaceholderTokens`.
- Tests: `rewrite_test.go`.

Read `README.md` before changing anything under `## Contracts`.
