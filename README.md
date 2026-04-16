# Known Limitations

This document only catalogs the application's known limitations. It is not an
overview, an install guide, or a usage reference.

Each entry points to the code that exhibits the behavior. A limitation listed
here is intentional, accepted, or not yet fixed — but it has been observed and
verified, not guessed.

## Process & environment

### 1. Interactive prompts require a TTY

`internal/ui/prompt.go` uses `charm.land/huh/v2` forms for category
selection and placeholder resolution. Piping cc-port into another process,
running it in CI, or invoking it from a shell script without a controlling
terminal will either block on input the process never receives or fail at
the form's `Run()`. There is no non-interactive / `--yes` mode; the only
way to automate is the two-step manifest flow (`export manifest` /
`import --from-manifest`), which sidesteps the prompts entirely.

## Move

### 2. A path immediately followed by `.` is left untouched

`internal/rewrite/rewrite.go:ReplacePathInBytes` treats `[A-Za-z0-9_.-]` as
path-component bytes. This protects prefix collisions like `myproject` vs
`myproject.v2`. The trade-off: prose such as `"look at
/Users/x/myproject."` is not rewritten — the trailing `.` is
indistinguishable from the start of an extension.

## Export

### 3. History is filtered by exact `project` field equality

`internal/export/export.go:extractProjectHistory` only includes lines whose
`project` field equals the requested project path. History entries that
reference the project only in `display` or `pastedContents` are excluded
from the export.

### 4. Binary detection is heuristic, not exhaustive

`internal/rewrite/rewrite.go:IsLikelyText` classifies content by scanning
three 512-byte windows (head, middle, tail) for a `\x00` byte and by
matching a small magic-prefix shortlist (PNG, JPEG, PDF, ZIP, gzip). The
heuristic gates both export anonymization and `move.Apply`'s file-history
snapshot rewrite. Two residual risks remain:

- **False negatives** — a binary format outside the magic shortlist (RAR,
  7z, custom containers) whose three windows are all null-free is still
  treated as text and substring-rewritten, which corrupts it.
- **False positives** — a text file whose middle or tail 512 bytes happen
  to contain a `\x00` (rare but possible in Unicode-escape-heavy content
  or synthetic fixtures) is classified as binary and skipped, leaving
  old paths unanonymised in an export or unrewritten in a move.

## Import

### 5. Same-filesystem requirement for atomic import

`internal/importer/importer.go:Run` uses a stage-and-swap strategy:
every destination is staged at a `*.cc-port-import.tmp` sibling path and
promoted via `os.Rename`. `os.Rename` is atomic only within a single
filesystem. On the common macOS and Linux layout where `~/.claude/`,
`~/.claude.json`, and `~/.claude/file-history/` all live on the user's
home filesystem, this holds. If any of these paths is a symlink across a
filesystem boundary (e.g. `~/.claude/file-history` pointed at an external
volume), the promote step fails with `EXDEV` and the import aborts —
correctly leaving no partial state — but the user sees a rename error
that may look unfamiliar.

### 6. `FindPlaceholderTokens` recognises only `{{[A-Z0-9_]+}}`

`internal/rewrite/rewrite.go:FindPlaceholderTokens` scans archive bodies
for placeholder tokens using a byte-walk matching `{{[A-Z0-9_]+}}`. This
is the shape cc-port's export path always emits, so the pre-flight gate
correctly classifies every archive this tool produces. Archives hand-
crafted with lowercase keys, punctuation inside braces, whitespace, or
multi-line tokens are invisible to the gate: such a token would neither
be flagged as undeclared nor be substituted at resolution time, so it
would survive verbatim on disk.

## Path encoding scope

cc-port identifies every project by its encoded directory name under
`~/.claude/projects/`. The encoding is inherited from Claude Code:
the input path is first resolved through the filesystem (following
symlinks), then `/`, `.`, and space are each replaced with `-`, and a `-`
is prepended. It is lossy — three distinct paths collapse to the same
name:

- `/Users/x/Projects/my project`
- `/Users/x/Projects/my-project`
- `/Users/x/Projects/my.project`

All three encode to `-Users-x-Projects-my-project`. cc-port uses the same
encoding (and the same symlink resolution on user-supplied paths) because
the encoded name must match what Claude Code writes on disk; the original
path cannot be recovered from the encoded form.

Refused by cc-port — these operations abort before touching anything:

- `cc-port move` (apply or dry-run) where old and new paths encode to the
  same directory name. The copy-and-delete sequence cannot run against a
  single on-disk location, and proceeding would destroy data.
- `cc-port move` (apply or dry-run) where the target encoded directory
  already exists. Another real project path has claimed that storage;
  proceeding would silently merge or overwrite its data.
- `cc-port import` where the target encoded directory already exists.
  Same reasoning.

Not covered — cases cc-port cannot detect or mitigate:

- **Pre-existing collisions.** If two distinct paths were already stored
  in the same encoded directory before cc-port ran — because Claude Code
  itself wrote both there — the data is interleaved and cc-port cannot
  untangle it. Operations targeting either path will read and write the
  shared storage.
- **Decoding a directory name back to a path.** One encoded name maps to
  any of several real paths. cc-port never tries to decode; every
  operation takes the original path as input and encodes forward. To find
  the owner of a stored directory, read `cwd` from a `sessions/*.json`
  file or the matching `~/.claude.json` project key.

## Rules files scope

cc-port treats `~/.claude/rules/*.md` as user-scoped guidance that should
stay untouched by a project move. Rules live one directory up from any
single project; if a rule needs a project path, the rule belongs inside
the project (e.g. `CLAUDE.md` at the project root), not in the global
rules directory. An in-place rewrite under `~/.claude/rules/` would
silently edit content the user likely wants reviewed by hand.

Scanned but not rewritten — `move` surfaces these so the user can edit
them manually:

- `cc-port move` (apply or dry-run) runs `internal/scan/rules.go:Rules`
  over every `.md` file in `~/.claude/rules/` and reports each line that
  contains the old project path as a `Warning` alongside the rest of the
  plan output. The files on disk are not modified.

Not covered — cases cc-port does not address:

- **Files outside `~/.claude/rules/`.** Only the top-level `.md` files in
  that directory are scanned. Nested subdirectories, non-`.md` extensions,
  and rules kept anywhere else on the system are ignored.
- **Automatic rewrite.** There is no `--rewrite-rules` flag. The warning
  is the entire remediation surface; the user is expected to inspect each
  hit and decide whether editing it, leaving it, or moving the rule into
  the project is the right call.

## Malformed history entries scope

`~/.claude/history.jsonl` is expected to be one JSON object per line. If
Claude Code wrote a partial line (crash, disk full, concurrent-write
race) or another tool corrupted the file, some lines will fail to parse.
These entries predate any cc-port invocation; the move did not create
them and cannot reconstruct the intended data from what was written.
Repairing them is out of scope — cc-port is a relocator, not a history
repair tool.

Surfaced by cc-port — both paths report malformed lines with their
1-based line numbers so the user can inspect or delete them manually:

- `cc-port move` (dry-run) includes a `Warning: history.jsonl has N
  malformed line(s) at […]` block in the plan output when any entries
  fail to parse.
- `cc-port move --apply` prints the same warning to stderr (or to the
  `move.Options.WarningWriter` supplied by callers) after the rewrite
  completes. The rewrite still succeeds — malformed lines are preserved
  verbatim, well-formed lines are rewritten normally.

Not covered — cases cc-port deliberately does not address:

- **Automatic repair.** cc-port does not attempt to reconstruct a broken
  line, drop it, quarantine it, or re-parse fragments. The original
  bytes land back on disk unchanged.
- **Detection outside `history.jsonl`.** The same class of corruption
  can in principle affect session transcripts (`*.jsonl` under the
  project directory) or session subdir files, but cc-port does not scan
  those for parse errors — they are rewritten as opaque byte streams
  with path-boundary-aware substitution.

## Import contract scope

`cc-port import` treats every archive as a closed contract: every
placeholder token a body contains must be accounted for before any
destination is written. The pre-flight gate in
`internal/importer/importer.go:Run` scans every ZIP entry, diffs against
the manifest's declared placeholders and the caller's resolutions, and
refuses the import on any mismatch. The rollback surface (see below)
means a refused import leaves the destination untouched — no partial
writes, no dangling staging temps.

Atomicity — every destination is staged at a sibling
`*.cc-port-import.tmp` path and promoted via `os.Rename`:

- `<encoded-project-dir>.cc-port-import.tmp` → `<encoded-project-dir>`
- `~/.claude/history.jsonl.cc-port-import.tmp` → `~/.claude/history.jsonl`
- `~/.claude.json.cc-port-import.tmp` → `~/.claude.json`
- per-entry file-history temps → their final `~/.claude/file-history/…`
  destinations

`internal/rewrite/rewrite.go:SafeRenamePromoter` drives the promote and
owns the rollback: if any rename step fails, every earlier rename is
reversed from the saved pre-promote bytes of each replaced destination.

Refused by cc-port — these paths abort before any write:

- Archive declares a placeholder marked `Resolvable: true` (or
  unspecified) whose key is not in `Options.Resolutions` and is not
  cc-port's implicit `{{PROJECT_PATH}}`. The error lists every missing
  key in alphabetical order.
- Archive body contains a `{{KEY}}` that the manifest does not declare
  at all. The error lists every undeclared key in alphabetical order.

Allowed-to-remain-symbolic — a placeholder marked `Resolvable: false` in
the manifest stays verbatim on disk even if no resolution was supplied.
This is the explicit escape hatch for "the sender acknowledges this
path has no meaning on the recipient's machine".

Not covered — cases cc-port does not address:

- **Pre-refactor archives with implicit unresolved keys.** Archives
  written by older cc-port versions whose manifest declared
  `{{KEY}}` (with `Resolvable: nil`, now meaning "must be resolved")
  without the caller supplying `{{KEY}}` are now refused. Migration:
  supply the resolution, or re-export with the key marked
  `Resolvable: false`.
- **Exotic token shapes.** See limitation #6 — only
  `{{[A-Z0-9_]+}}` is recognised by the pre-flight scanner.

## Concurrency guard scope

Before mutating shared files under `~/.claude/`, cc-port acquires an
exclusive advisory lock on `~/.claude/.cc-port.lock` and scans
`~/.claude/sessions/*.json` for entries whose recorded `pid` is alive on
the host. If either check finds something, the invocation aborts before
any files are touched. The kernel releases the lock when cc-port exits,
so a crash does not leave a stale block on the next invocation.

Guarded commands — these take the lock and run the live-session check:

- `cc-port move --apply`
- `cc-port import`

Not guarded — these are read-only with respect to `~/.claude/` and run
without locking or session detection:

- `cc-port move` (dry-run) — counts potential replacements without
  writing. A concurrent Claude Code write can skew the reported counts
  but cannot corrupt data.
- `cc-port export` and `cc-port export manifest` — read from
  `~/.claude/` and write only to the output archive or manifest file
  outside it. A concurrent Claude Code write during a long export can
  produce an internally inconsistent archive (e.g. a history snapshot
  that does not line up with a transcript snapshot), but nothing under
  `~/.claude/` changes.
