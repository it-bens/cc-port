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
  - `PromoteDir(ctx context.Context, source, destination string, undo undoRegistrar, copyDir func(context.Context, string, string, func()) error) error`:
    stages `source` into a sibling `.cc-port-staging.tmp` directory beside
    `destination`, writes a promotion marker recording `source` inside the
    staging directory, then renames it into place. The directory-promotion
    counterpart to `SafeRenamePromoter`, used by `move`'s project-directory surface.
  - `VerifyPromotedFrom(source, destination string) (bool, error)`:
    reports whether `destination` carries a marker recording exactly `source`.
  - `RemoveMarker(destination string) error`: removes `destination`'s
    promotion marker; a missing marker is not an error.

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

Codex stores project paths inside TOML table keys: the canonical
`[projects."<path>"]` trust table, and any project-local
`[hooks.state."<path>:<event>:<group>:<handler>"]` entry whose hook source
sits under the project. Codex preserves comments and formatting when it edits
its own config. Go has no `toml_edit`-equivalent library, and re-emitting a
parsed document would destroy that formatting, so `TOMLPathRewrite` performs
the same boundary-aware byte replacement as `ReplacePathInBytes` and then
validates the result rather than re-serializing it: input parses as TOML,
output parses as TOML, and the multiset of full dotted key paths matches the
one produced by applying the project-path substitution to every key segment.
A key carrying the old project path is expected to change; a key that changes
any other way fails the check.

#### Handled

- A project table key (`[projects."/old/path"]`) and a project path value
  elsewhere in the document (for example under `[hooks]`) both rewrite in
  one call, with every comment and blank line preserved verbatim.
- A project-local `hooks.state` key whose hook-source prefix is the old
  project path: rewritten exactly as its bytes are, so the entry stays
  addressable under the moved project. Any table keyed by the project path is
  covered this way, with no enumerated table list; a user-level hook key
  outside the project keeps its path.
- The multiset walks every array-of-tables element under its parent key path,
  so a path key inside a `[[...]]` block rewrites and validates the same way.

#### Refused

- Paths containing a quote (`"`) or a backslash (`\`): `TOMLPathRewrite`
  refuses before attempting any rewrite, since a TOML basic-string key
  containing either character would require escaping this primitive does
  not implement.
- A rewrite whose output key-path multiset differs from the expected one: a
  key changed by anything other than the project-path substitution. Refused
  with no partial write, the original bytes returned unchanged.
- A rewrite whose output no longer parses as TOML, such as a collision that
  fuses two project keys into a duplicate table: refused by the output
  re-parse, again with the original bytes returned unchanged.

#### Not covered

- TOML documents this package's parser (`github.com/pelletier/go-toml/v2`,
  used for validation only) cannot parse. `TOMLPathRewrite` refuses such
  input rather than guessing at a byte-level rewrite against an
  unparseable document.

### Directory promotion

`PromoteDir` copies `source` into a sibling `<destination>.cc-port-staging.tmp`
directory, writes `.cc-port-promoted-from` inside the staging directory, then
renames it onto `destination`. The rename publishes a fully marked destination
in one operation, and a mid-copy failure never leaves a partial directory at
the real destination path. `move`'s project-directory surface is its only caller
([`internal/tool/claude/README.md`](../tool/claude/README.md) §Apply
contract (move)); `import` still promotes through `SafeRenamePromoter`, not
this primitive.

The promoted marker records `source` at
`<destination>/.cc-port-promoted-from`. `VerifyPromotedFrom` resumes only
when that content exactly matches `source`, regardless of the marker's mtime.

#### Handled

- A destination that does not yet exist: staged copy, then an atomic
  `os.Rename` onto `destination`.
- A destination carrying a marker that names exactly `source`: the caller
  treats the promotion as already done and skips the copy.
- Rollback via the caller's `undo.RegisterUndo`: before the rename, removes
  the staging directory; after the rename, removes `destination` with its
  marker.

#### Refused

- A marker that names a different source: `VerifyPromotedFrom` returns false
  rather than treating it as valid.

#### Not covered

- Classifying whether `destination` is safe to promote into. `PromoteDir`
  performs no destination-existence check itself; the caller decides
  promote vs. resume vs. refuse before invoking it
  ([`internal/tool/claude/README.md`](../tool/claude/README.md) §Apply
  contract (move)).

## Tests

Unit tests in `rewrite_test.go` cover `PromoteDir` and promotion markers
(inside-destination placement, content verification, arbitrarily old markers,
and rollback), `ReplacePathInBytes` (including dot-boundary lookahead),
`SafeRenamePromoter` (files, dirs, rollback path), `EscapeSJSONKey`,
`ContainsBoundedPath`, the `Count*` primitives (boundary cases, the
JSON-escaped form, and parity with their `Replace*` counterparts), and
`SafeWriteFile`.

Unit tests in `toml_test.go` cover `TOMLPathRewrite`: table-key and
value-position renames, comment and formatting preservation, project-local
`hooks.state` key rewrites (with a user-level hook key left untouched and a
nested array-of-tables key), the refusal on a colliding key rewrite, and the
quote/backslash input refusal.

Fuzz target in `rewrite_fuzz_test.go`. `FuzzReplacePathInBytes` asserts
empty-`oldPath` no-op, identity-rewrite byte equality, and the length
invariant. Seed inputs run as deterministic subtests under `go test ./...`.
See `DEVELOPMENT.md §Tests and lint` for the unbounded-mutation invocation.
