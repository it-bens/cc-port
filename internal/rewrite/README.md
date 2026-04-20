# internal/rewrite

## Purpose

Byte-level path-rewrite primitives and atomic-rename orchestration for cc-port.
Every path-rewriting command routes through this package so the boundary contract is enforced in one place.

## Public API

- **Core replacement**
  - `ReplacePathInBytes(data []byte, oldPath, newPath string) ([]byte, int)`: boundary-aware substring replace; returns rewritten bytes and match count.
  - `ContainsBoundedPath(data []byte, path string) bool`: same boundary check without rewriting.
  - `EscapeSJSONKey(key string) string`: escapes a key for use as a single segment in an sjson path expression.
- **Typed file helpers**
  - `HistoryJSONL(data []byte, oldProject, newProject string) ([]byte, int, []int, error)`: rewrites `history.jsonl`; returns rewritten bytes, changed-line count, malformed-line numbers, and error.
  - `SessionFile(data []byte, oldProject, newProject string) ([]byte, bool, error)`: rewrites a session JSON file.
  - `UserConfig(data []byte, oldProject, newProject string) ([]byte, bool, error)`: rewrites `~/.claude.json`.
- **Placeholder scanning**
  - `FindPlaceholderTokens(data []byte) []string`: tamper-defense scan for undeclared `{{UPPER_SNAKE}}` tokens.
- **Atomic rename**
  - `NewSafeRenamePromoter() *SafeRenamePromoter`: constructor for the staged-write promoter used by `import`.
  - `SafeRenamePromoter` type with methods:
    - `StageFile`, `StageDir`: register destinations.
    - `Promote`: runs the rename chain.
    - `Rollback`: reverses completed renames.
    - `SetRenameFunc`: injects a test hook.
  - `SafeWriteFile(path string, data []byte, permissions os.FileMode) error`: write-then-rename helper for single-file atomic writes.

## Contracts

### Boundary rules

Every path substitution in cc-port (`move`, `export` anonymization, `import`
placeholder resolution) runs through `ReplacePathInBytes`. A bare substring
replace would corrupt unrelated paths sharing a prefix with the old project
path (`/Users/x/myproject` inside `/Users/x/myproject.v2`). To prevent this,
the function requires each match to be bounded on the right by a byte that
cannot extend a path component.

Path-component bytes are `[A-Za-z0-9_-]`. The `.` byte uses a two-byte
lookahead because it appears both as an extension separator (`.v2`, `.txt`)
and as sentence-ending punctuation. A `.` immediately after a match blocks the
rewrite only when the first non-dot byte that follows is a path-component byte.
Otherwise the dot is prose and the rewrite proceeds.

#### Handled

- `/a/foo` followed by a non-path byte (whitespace, `/`, `"`, `,`, `!`, `?`,
  `;`, `:`, end of buffer, etc.).
- `/a/foo` followed by `.` and then a non-path byte: sentence-terminating
  prose (`"look at /a/foo."`, `see /a/foo. Also see /a/foo`).
- `/a/foo` followed by a run of dots and then a non-path byte: ellipsis
  (`see /a/foo... done`).

#### Refused

- `/a/foo` immediately followed by another path-component byte
  (`/a/foo-extras`, `/a/foo2`, `/a/foo_bar`): a different path.
- `/a/foo` followed by `.` and then a path-component byte: an extension
  (`/a/foo.v2`, `/a/foo.txt`, `/a/foo.git`, `/a/foo.2`, `/a/foo._hidden`,
  `/a/foo.-weird`).

#### Not covered

Directories whose final component ends in a literal trailing `.` or `..`
(e.g. `/a/foo.`) are rewritten when followed by a word-boundary byte. A
distinct unrelated project named `/a/foo.` would have been preserved by
one-byte boundary checking alone. These names are pathological on Unix and
forbidden on Windows. cc-port accepts this trade-off in favour of correctly
rewriting sentence-ending prose references.

### Placeholder scanning

`FindPlaceholderTokens` is the tamper-defense scan used by the importer to
refuse archives whose bodies carry `{{UPPER_SNAKE}}` tokens the manifest never
declared. See `internal/importer/README.md §Placeholder handling` for the
full role.

#### Handled

`FindPlaceholderTokens` matches tokens of the form `{{[A-Z0-9_]{1,64}}}` in
first-occurrence order, returning each token with its surrounding braces
(e.g. `{{PROJECT_PATH}}`).

#### Refused

Tokens outside the upper-snake grammar are ignored: lowercase keys,
punctuation, whitespace inside braces, nested braces, multi-line keys.
cc-port's export path only writes upper-snake keys. Matching anything wider
produces false positives on legitimate `{{...}}` content in transcripts
(Handlebars, Mustache, Jinja).

#### Not covered

Hand-crafted archives that embed placeholder keys in non-upper-snake shapes
must list every embedded key in the manifest. Tool-produced archives are not
affected because cc-port's export path declares every key it embeds.

## Tests

Unit tests in `rewrite_test.go` cover `HistoryJSONL`, `ReplacePathInBytes`
(including dot-boundary lookahead), `SessionFile`, `UserConfig`,
`FindPlaceholderTokens`, `SafeRenamePromoter` (files, dirs, rollback path),
`EscapeSJSONKey`, `ContainsBoundedPath`, and `SafeWriteFile`.

Fuzz targets in `rewrite_fuzz_test.go`. `FuzzReplacePathInBytes` asserts
empty-`oldPath` no-op, identity-rewrite byte equality, and the length
invariant. `FuzzFindPlaceholderTokens` asserts distinct tokens, conformance to
`{{[A-Z0-9_]{1,64}}}`, and substring presence in the input. Seed inputs run
as deterministic subtests under `go test ./...`. See `DEVELOPMENT.md §Tests
and lint` for the unbounded-mutation invocation.
