# internal/rewrite

## Purpose

Byte-level path-rewrite primitives and atomic-rename orchestration for cc-port.
Every path-rewriting command routes through this package so the boundary contract is enforced in one place.

## Public API

- **Core replacement**
  - `ReplacePathInBytes(data []byte, oldPath, newPath string) ([]byte, int)`: boundary-aware substring replace; returns rewritten bytes and match count.
  - `ReplacePathInBytesWithJSONEscape(data []byte, oldPath, newPath string) ([]byte, int)`: two-pass variant that also matches the JSON-escaped `\/` form. Byte-identical to `ReplacePathInBytes` when the input contains no escaped slashes.
  - `ContainsBoundedPath(data []byte, path string) bool`: same boundary check without rewriting.
  - `CountPathInBytes(data []byte, path string) int`: counts bounded occurrences without rewriting, scanning without a rewritten copy. The counting analogue of `ReplacePathInBytes`.
  - `CountPathInBytesWithJSONEscape(data []byte, path string) int`: counts bounded occurrences across both the raw and JSON-escaped forms. The counting analogue of `ReplacePathInBytesWithJSONEscape`.
  - `EscapeSJSONKey(key string) string`: escapes a key for use as a single segment in an sjson path expression.
- **TOML rewrite**
  - `TOMLPathRewrite(data []byte, oldPath, newPath string) (rewritten []byte, count int, err error)`: rewrites bounded path references in raw TOML while preserving the original formatting and comments.
- **Atomic rename**
  - `NewSafeRenamePromoter() *SafeRenamePromoter`: constructor for the staged-write promoter used by `import`.
  - `SafeRenamePromoter` type with methods:
    - `StageFile`: registers a destination.
    - `Promote`: runs the rename chain.
    - `Rollback`: reverses completed renames.
    - `SetRenameFunc`: injects a test hook.
  - `SafeWriteFile(path string, data []byte, permissions os.FileMode) error`: write-then-rename helper for single-file atomic writes.

## Contracts

### Boundary rules

Every raw-path substitution in cc-port (`move`'s rewrites, `export`'s
anonymization) runs through `ReplacePathInBytes`. A bare substring replace
would corrupt unrelated paths sharing a prefix with the old project path
(`/Users/x/myproject` inside `/Users/x/myproject.v2`). To prevent this, the
function requires each match to be bounded on the right by a byte that
cannot extend a path component.

Import placeholder resolution is a different mechanism and does not go
through this function: it substitutes self-delimiting `{{UPPER_SNAKE}}`
tokens via `archive.ResolvePlaceholdersStream` (streaming) or
`archive.ApplyResolutions` (in-memory), where the token's own `}}` suffix
makes a boundary check unnecessary (see
[`internal/archive/README.md`](../archive/README.md) §Placeholder machinery).

Path-component bytes are `[A-Za-z0-9_-]`. The `.` byte uses a two-byte
lookahead because it appears both as an extension separator (`.v2`, `.txt`)
and as sentence-ending punctuation. A `.` immediately after a match blocks the
rewrite only when the first non-dot byte that follows is a path-component byte.
Otherwise the dot is prose and the rewrite proceeds.

JSON emitters that write `\/` instead of `/` defeat a single-pass raw-byte
rewriter. `ReplacePathInBytesWithJSONEscape` runs the raw pass first, then
a second pass keyed on `oldPath` and `newPath` with every `/` replaced by
`\/`. The boundary check applies independently to each pass, so
`\/Users\/me\/foo\/bar` still matches while `\/Users\/me\/foobar` does not.
Tool-specific helpers choose this variant when their JSON surfaces require it.
Callers rewriting raw filesystem bytes stay on `ReplacePathInBytes`.

`CountPathInBytes` and `CountPathInBytesWithJSONEscape` apply the identical
boundary rule but report only the match count; `stats` uses them to inventory
references without producing a throwaway rewritten copy. Their raw-vs-escape
split mirrors the replacers above.

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

### TOML boundary rules

Codex stores project paths inside TOML table keys (`[projects."<path>"]`)
and preserves comments and formatting when it edits its own config. Go has
no `toml_edit`-equivalent library, and re-emitting a parsed document would
destroy that formatting, so `TOMLPathRewrite` performs the same
boundary-aware byte replacement as `ReplacePathInBytes` and then validates
the result rather than re-serializing it: input parses as TOML, output
parses as TOML, and the multiset of full dotted key paths is unchanged
except the expected renames at rewritten `projects` sub-keys.

#### Handled

- A project table key (`[projects."/old/path"]`) and a project path value
  elsewhere in the document (for example under `[hooks]`) both rewrite in
  one call, with every comment and blank line preserved verbatim.
- The key-path-multiset check catches a rewrite that accidentally changed a
  key outside the `projects` table: `TestTOMLPathRewriteRejectsKeyChangesOutsideProjects`
  asserts a byte-level match that happens to land on a non-`projects` key
  name is refused rather than silently accepted.

#### Refused

- Paths containing a quote (`"`) or a backslash (`\`): `TOMLPathRewrite`
  refuses before attempting any rewrite, since a TOML basic-string key
  containing either character would require escaping this primitive does
  not implement.
- A rewrite whose output key-path multiset differs from the expected one
  (any key change outside the `projects` table): refused with no partial
  write, the original bytes returned unchanged.

#### Not covered

- TOML documents this package's parser (`github.com/pelletier/go-toml/v2`,
  used for validation only) cannot parse. `TOMLPathRewrite` refuses such
  input rather than guessing at a byte-level rewrite against an
  unparseable document.

## Tests

Unit tests in `rewrite_test.go` cover `ReplacePathInBytes` (including
dot-boundary lookahead), `SafeRenamePromoter` (files, dirs, rollback path), `EscapeSJSONKey`,
`ContainsBoundedPath`, the `Count*` primitives (boundary cases, the
JSON-escaped form, and parity with their `Replace*` counterparts), and
`SafeWriteFile`.

Unit tests in `toml_test.go` cover `TOMLPathRewrite`: table-key and
value-position renames, comment and formatting preservation, the
key-path-multiset refusal on a rewrite that touches a key outside
`projects`, and the quote/backslash input refusal.

Fuzz target in `rewrite_fuzz_test.go`. `FuzzReplacePathInBytes` asserts
empty-`oldPath` no-op, identity-rewrite byte equality, and the length
invariant. Seed inputs run as deterministic subtests under `go test ./...`.
See `DEVELOPMENT.md §Tests and lint` for the unbounded-mutation invocation.
