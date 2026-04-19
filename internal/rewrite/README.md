# internal/rewrite

## Purpose

Byte-level rewrite primitives: substring-safe path replacement and atomic rename orchestration. Every path-rewriting command in cc-port (`move`, `export` anonymization, `import` placeholder resolution) routes through this package so the path-boundary contract is enforced in one place.

Not a file-type aware rewriter — callers pass bytes and get bytes back. JSON / JSONL parsing lives in the typed helpers below, but the core replacement (`ReplacePathInBytes`) is byte-level by design.

## Public API

- **Core replacement**
  - `ReplacePathInBytes(data []byte, oldPath, newPath string) ([]byte, int)` — boundary-aware substring replace, returns rewritten bytes and match count.
  - `ContainsBoundedPath(data []byte, path string) bool` — same boundary check without rewriting.
  - `EscapeSJSONKey(key string) string` — escapes a key for use in raw JSON.
- **Typed file helpers**
  - `HistoryJSONL(data []byte, oldProject, newProject string) ([]byte, int, []int, error)` — rewrites `history.jsonl`, returns rewritten bytes, count, malformed-line numbers, and error.
  - `SessionFile(data []byte, oldProject, newProject string) ([]byte, bool, error)` — rewrites a session JSON file.
  - `UserConfig(data []byte, oldProject, newProject string) ([]byte, bool, error)` — rewrites `~/.claude.json`.
- **Placeholder scanning**
  - `FindPlaceholderTokens(data []byte) []string` — tamper-defense scan for undeclared `{{UPPER_SNAKE}}` tokens; see `internal/importer/README.md` §Placeholder resolution for the full role.
- **Atomic rename**
  - `NewSafeRenamePromoter() *SafeRenamePromoter` — constructor for the staged-write promoter used by `import`.
  - `SafeRenamePromoter` — type; `StageFile`, `StageDir` register destinations, `Promote` runs the rename chain, `Rollback` reverses completed renames, `SetRenameFunc` injects a test hook. Drives the atomic-import flow described in `internal/importer/README.md` §Atomic staging.
  - `SafeWriteFile(path string, data []byte, permissions os.FileMode) error` — write-then-rename helper for single-file atomic writes.

## Contracts

### Boundary rules

Every substring-level path substitution in cc-port (`move`, `export`
anonymization, `import` placeholder resolution) runs through
`internal/rewrite/rewrite.go:ReplacePathInBytes`. A bare substring replace
would corrupt unrelated paths that happen to share a prefix with the old
project path (`/Users/x/myproject` inside `/Users/x/myproject.v2` or
`/Users/x/myproject-extras`), so the function requires that each match be
bounded on the right by a byte that cannot extend a path component.

Path-component bytes are `[A-Za-z0-9_-]`. The `.` byte is handled by a
two-byte lookahead, because `.` appears both as an extension separator
(`.v2`, `.txt`) — where it must block the rewrite — and as prose
sentence-ending punctuation (`"look at /Users/x/myproject."`) — where it
must not. A `.` immediately after a candidate match is classified as an
extension separator only when the first non-dot byte that follows is
itself a path-component byte; otherwise (whitespace, quote, other
punctuation, EOF) the dot is prose and the rewrite proceeds.

Rewritten — these are safely treated as full-component matches:

- `/a/foo` followed by a non-path byte (whitespace, `/`, `"`, `,`, `!`,
  `?`, `;`, `:`, end of buffer, etc.).
- `/a/foo` followed by `.` and then a non-path byte: sentence-terminating
  prose (`"look at /a/foo."`, `see /a/foo. Also see /a/foo`).
- `/a/foo` followed by a run of dots and then a non-path byte: ellipsis
  (`see /a/foo... done`).

Not rewritten — the boundary check deliberately suppresses these:

- `/a/foo` immediately followed by another path-component byte
  (`/a/foo-extras`, `/a/foo2`, `/a/foo_bar`) — a different path.
- `/a/foo` followed by `.` and then a path-component byte — an extension
  (`/a/foo.v2`, `/a/foo.txt`, `/a/foo.git`, `/a/foo.2`, `/a/foo._hidden`,
  `/a/foo.-weird`).

## Quirks

### Trailing-dot path components

Directories whose final component ends in a literal trailing `.` or
`..` (e.g. a real path `/a/foo.`) are rewritten when followed by a
word-boundary byte, even though a distinct unrelated project named
`/a/foo.` would have been preserved by one-byte boundary checking.
These names are pathological on Unix and forbidden on Windows; cc-port
accepts this trade-off in favour of correctly rewriting sentence-ending
prose references.

### Placeholder-token grammar is narrow by design

`FindPlaceholderTokens` is the tamper-defense scan used by the importer
to refuse archives whose bodies carry `{{UPPER_SNAKE}}` tokens the
manifest never declared. The grammar is intentionally narrow — it only
matches upper-snake keys. Widening it to lowercase, punctuated, or
whitespace-bearing tokens would produce false positives on legitimate
`{{…}}` content embedded in transcripts (Handlebars, Mustache, Jinja).
Tool-produced archives are not affected because cc-port's export path
declares every key it embeds; hand-crafted archives that want the full
contract must list every embedded key in the manifest.

## Tests

Unit tests in `rewrite_test.go`. Coverage spans `HistoryJSONL`, `ReplacePathInBytes` (including the dot-boundary lookahead), `SessionFile`, `UserConfig`, `FindPlaceholderTokens`, `SafeRenamePromoter` (files + dirs, rollback path), `EscapeSJSONKey`, `ContainsBoundedPath`, and `SafeWriteFile`. Table-driven where multiple cases share the same assertion.
