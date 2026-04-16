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

## Import

### 2. `FindPlaceholderTokens` recognises only `{{[A-Z0-9_]+}}`

`internal/rewrite/rewrite.go:FindPlaceholderTokens` scans archive bodies
for placeholder tokens using a byte-walk matching `{{[A-Z0-9_]+}}`. This
is the shape cc-port's export path always emits, so the pre-flight gate
correctly classifies every archive this tool produces. Archives hand-
crafted with lowercase keys, punctuation inside braces, whitespace, or
multi-line tokens are invisible to the gate: such a token would neither
be flagged as undeclared nor be substituted at resolution time, so it
would survive verbatim on disk.

## Path-boundary rewrite scope

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

Residual risk — cases this heuristic does not perfectly cover:

- **Directories whose final component ends in a literal trailing `.` or
  `..`** (e.g. a real path `/a/foo.`) are rewritten when followed by a
  word-boundary byte, even though a distinct unrelated project named
  `/a/foo.` would have been preserved by one-byte boundary checking.
  These names are pathological on Unix and forbidden on Windows; cc-port
  accepts this trade-off in favour of correctly rewriting sentence-ending
  prose references.

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

## File-history scope

cc-port treats every file under `~/.claude/file-history/<session-uuid>/`
as an opaque byte stream. The directory is indexed by session UUID (not
by project path), and each `<hash>@vN` entry is a verbatim copy of a
file the user edited through Claude Code — the in-session rewind feature
uses it by filename, not by content. Any project-path string that
appears inside a snapshot body is coincidental (log line, comment,
string literal) and not load-bearing, so cc-port never inspects or
rewrites snapshot contents.

Preserved byte-for-byte — these operations copy snapshot bodies verbatim
and surface a one-line warning so the behaviour is not a surprise:

- `cc-port move` (apply or dry-run) leaves every snapshot under the same
  UUID directory untouched. The old project path may still appear inside
  a snapshot body afterwards; the apply path prints
  `warning: N file-history snapshot(s) preserved as-is …` to stderr (or
  to `move.Options.WarningWriter`) and the dry-run plan reports the
  preserved count in the same position.
- `cc-port export` (with the `file-history` category enabled) writes
  each snapshot verbatim into the archive under `file-history/<uuid>/…`.
  No path anonymisation runs over those bytes. The CLI prints
  `Warning: N file-history snapshot(s) archived as-is …` to stderr when
  the count is positive.
- `cc-port import` writes snapshots back to disk as the opaque bytes the
  archive carried. `ResolvePlaceholders` still runs over every entry for
  compatibility with older archives (a `{{KEY}}` that somehow survived
  inside a body will still be substituted), but on snapshots produced by
  current cc-port the pass is a no-op because no tokens are present.

Not covered — cases cc-port deliberately does not address:

- **Stale path strings inside snapshots after a move.** Grepping
  `~/.claude/file-history/` for the old project path still returns hits
  after a successful move. This is by design: editing snapshot bytes
  means substring-rewriting arbitrary user-file content, which is the
  class of risk the previous binary-detection heuristic tried (and
  sometimes failed) to guard against. Rewind continues to work because
  it resolves by filename, not by content.
- **Privacy of exported snapshots.** An archive shared with someone else
  carries the sender's literal project path inside any snapshot that
  quoted it. If a recipient must not see that path, the `file-history`
  category has to be excluded up front — `--file-history=false`, the
  absence of `--file-history` when other `--<category>` flags are set,
  or unchecking the category in the interactive prompt. There is no
  scrub pass between export and archive creation; the category flag is
  the entire opt-out surface.
- **Decoding snapshot UUIDs back to a project.** Snapshot directories
  are named by session UUID. To find the owner of a UUID directory,
  read the matching `~/.claude/sessions/<uuid>.json` and look at its
  `cwd` field — cc-port does not index file-history in the reverse
  direction.

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

## Atomic import staging scope

`cc-port import` makes every destination visible all-or-nothing by
staging each write at a sibling `*.cc-port-import.tmp` path and
promoting it with `os.Rename`. `os.Rename` is atomic only within a
single filesystem, and a bare-sibling temp path would sit on the
wrong side of the boundary whenever a destination's parent is a
symlink to another volume (a common layout for
`~/.claude/file-history` pointed at an external disk), so the
promote step would fail mid-import with `EXDEV`.

`internal/importer/importer.go:stagingTempPath` resolves the parent
directory of each final destination through any symlinks before
forming the temp path, so temp and final are siblings of the
*resolved* parent and therefore always share a filesystem. The walk
handles missing trailing components the same way
`internal/claude/paths.go:ResolveProjectPath` handles nonexistent
project paths: the longest existing prefix is symlink-resolved, and
any missing tail is re-attached unchanged so `MkdirAll` creates it on
the resolved filesystem at stage time.

`internal/importer/importer.go:checkStagingFilesystems` runs this
resolution once up front for every destination the importer will
touch (the encoded project directory, `history.jsonl`,
`.claude.json`, and the file-history base) and aggregates any
failures into a single error before the archive is read or any temp
is written. This turns an obscure mid-promote rename failure into a
clear "resolve staging parent for X" message that fires before the
import has touched anything.

Handled — layouts where promotion stays atomic:

- All four destinations on the same filesystem (the common macOS and
  Linux layout with everything under the home directory).
- Any subset of destinations whose *parent directory* is a symlink
  crossing a filesystem boundary (e.g. `~/.claude/file-history`
  pointed at an external volume). The temp is staged on the external
  volume alongside its final, and `os.Rename` remains intra-filesystem.
- Destinations whose parent directory does not exist yet. The
  ancestor walk finds the closest existing prefix, resolves it, and
  `MkdirAll` creates the missing components on that filesystem.

Refused before any write — these paths abort at preflight with a
single aggregated error:

- A destination's symlinked parent is broken or otherwise
  unresolvable (`EvalSymlinks` returns a non-`ENOENT` error).
- A destination's parent ancestor walk fails with a non-`ENOENT`
  stat error (permission denied on an intermediate component, etc.).

Not covered — cases this approach deliberately does not address:

- **Final destination is itself a cross-filesystem symlink.** If
  `~/.claude/projects/<encoded>`, `~/.claude/history.jsonl`, or
  `~/.claude.json` already exists as a symlink whose target lives on
  a different filesystem than the symlink's parent,
  `CheckConflict`/merge refuses or overwrites based on existing-file
  rules, not on symlink topology. For the project directory
  specifically, `CheckConflict` refuses when the encoded directory
  already exists, so a pre-existing symlinked leaf does not reach
  the rename. A symlinked `history.jsonl` or `.claude.json` leaf
  would still route through `os.Rename` on the symlink's parent
  filesystem; if the symlink itself straddles a boundary the
  promote fails and the rollback surface (see **Import contract
  scope**) restores pre-import state.
- **Filesystem topology changes mid-import.** The preflight resolves
  parents once. A concurrent operation that replaces a resolved
  parent with a cross-filesystem symlink between preflight and
  promote can still produce `EXDEV` at rename time; the promoter
  rolls back and the import aborts, but the friendly preflight
  error does not fire.

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
- **Exotic token shapes.** See limitation #3 — only
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
