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

## Path encoding

### 2. The encoder is lossy and irreversible

`internal/claude/paths.go:22-29` collapses `/`, `.`, and ` ` to `-` and
prepends a `-`. Three distinct project paths can encode to the same directory
name:

- `/Users/x/Projects/my project`
- `/Users/x/Projects/my-project`
- `/Users/x/Projects/my.project`

All three become `-Users-x-Projects-my-project`. The original cannot be
recovered from the encoded form — recovery requires reading `cwd` /
`projectPath` from inside the project's data files.

## Move

### 3. `~/.claude.json` is read, re-marshaled, and rewritten as a whole on every run

`internal/rewrite/rewrite.go:UserConfig` and
`internal/importer/importer.go:mergeProjectConfig` both load the entire
`~/.claude.json` into memory, unmarshal it, mutate the `projects` map, and
write the whole file back. For users with many projects this file runs to
tens of thousands of lines. Three consequences: the operation's cost scales
with the size of the whole user config, not with the single project being
moved; any formatting that Claude Code itself uses — key ordering, indent
style, trailing newlines — is rewritten to Go's `encoding/json` output on
every operation; and the two paths disagree on output format — `move`
writes compact single-line JSON via `json.Marshal`, while `import` writes
two-space-indented JSON via `json.MarshalIndent`, so back-to-back
operations on the same file produce visibly different layouts.

### 4. `~/.claude/file-history/<session-uuid>/<hash>@vN` snapshots are never rewritten

`internal/move/move.go:Apply` rewrites transcripts, memory, history, session
files, settings, and config. It does not call any rewriter on file-history
snapshot contents. Snapshots that captured file contents containing the old
project path remain stale.

### 5. Rules files are scanned, never rewritten

`internal/scan/rules.go:Rules` reports occurrences of the old path as
`Warning`s. `move.Apply` does not modify anything under `~/.claude/rules/`.
Rules that hard-code the old project path require manual editing after a
move.

### 6. A path immediately followed by `.` is left untouched

`internal/rewrite/rewrite.go:ReplacePathInBytes` treats `[A-Za-z0-9_.-]` as
path-component bytes. This protects prefix collisions like `myproject` vs
`myproject.v2`. The trade-off: prose such as `"look at
/Users/x/myproject."` is not rewritten — the trailing `.` is
indistinguishable from the start of an extension.

### 7. Malformed history lines are preserved silently

`internal/rewrite/rewrite.go:HistoryJSONL` keeps malformed lines verbatim and
continues. The user receives no warning that those lines exist or that the
rewriter could not touch them.

## Export

### 8. History is filtered by exact `project` field equality

`internal/export/export.go:extractProjectHistory` only includes lines whose
`project` field equals the requested project path. History entries that
reference the project only in `display` or `pastedContents` are excluded
from the export.

### 9. Binary detection uses a 512-byte null-byte heuristic

`internal/export/export.go:isLikelyText` checks only the first 512 bytes for
a `\x00` byte. Files that are binary after a textual header, or binary
formats that happen to start with non-null bytes, are treated as text and
substring-rewritten — which corrupts them.

### 10. The export anonymizer is not path-boundary aware

`internal/export/export.go:anonymize` uses `strings.ReplaceAll`. It is
currently safe only because placeholders are sorted by `Original` length
descending, which handles the common HOME-prefix-of-PROJECT case. Adding
placeholders whose `Original` strings are substrings of each other in any
other order can corrupt output.

## Import

### 11. Import has no atomic or rollback guarantee

`internal/importer/importer.go:Run` streams ZIP entries and writes each to
its final destination as it goes: files into the encoded project directory,
appends to `~/.claude/history.jsonl`, an in-place rewrite of
`~/.claude.json`. A failure midway — out of disk, permission error, a
corrupt entry, a missing resolution — leaves some destinations written and
others not, with no equivalent of `move.Apply`'s copy-verify-delete
strategy. Rolling back a partial import is manual.

### 12. Unsupplied placeholders survive import as literal strings

`internal/importer/importer.go:Run` only resolves placeholders the caller
provided in `Options.Resolutions`. If the archive's `metadata.xml` declares
a placeholder the caller did not supply, the literal `{{KEY}}` string
remains in every imported file — there is no validation gate.

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
