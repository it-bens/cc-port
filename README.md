# Known Limitations

This document only catalogs the application's known limitations. It is not an
overview, an install guide, or a usage reference.

Each entry points to the code that exhibits the behavior. A limitation listed
here is intentional, accepted, or not yet fixed — but it has been observed and
verified, not guessed.

## Process & environment

### 1. A concurrent Claude Code session is not detected

The tool does not check whether Claude Code is currently running against the
same `~/.claude/` directory. `move` and `import` both read, mutate, and
rewrite shared files — `history.jsonl`, `~/.claude.json`, `settings.json` —
in non-locking passes. A Claude Code process writing the same files in
parallel will race with cc-port and the last writer wins. There is no
advisory lock, no PID check, no warning.

### 2. Interactive prompts require a TTY

`internal/ui/prompt.go` uses `charm.land/huh/v2` forms for category
selection and placeholder resolution. Piping cc-port into another process,
running it in CI, or invoking it from a shell script without a controlling
terminal will either block on input the process never receives or fail at
the form's `Run()`. There is no non-interactive / `--yes` mode; the only
way to automate is the two-step manifest flow (`export manifest` /
`import --from-manifest`), which sidesteps the prompts entirely.

## Path encoding

### 3. Symlinks in the caller-supplied path are not resolved

`EncodePath` in `internal/claude/paths.go:16-23` applies its replacement
rules to the string as given. Claude Code itself resolves the path through
the filesystem first — on macOS, a project started under `/tmp/foo` is
stored as `-private-tmp-foo`, not `-tmp-foo`, because `/tmp` symlinks to
`/private/tmp`. If the user hands `cc-port move` an unresolved symlink
path, the encoded directory name will not match what Claude Code wrote and
`LocateProject` returns "project directory not found". The caller is
required to pass the fully resolved absolute path.

### 4. The encoder is lossy and irreversible

`internal/claude/paths.go:16-23` collapses `/`, `.`, and ` ` to `-` and
prepends a `-`. Three distinct project paths can encode to the same directory
name:

- `/Users/x/Projects/my project`
- `/Users/x/Projects/my-project`
- `/Users/x/Projects/my.project`

All three become `-Users-x-Projects-my-project`. The original cannot be
recovered from the encoded form — recovery requires reading `cwd` /
`projectPath` from inside the project's data files.

### 5. No collision detection across encoded directories

The tool does not warn when two real project paths encode to the same
directory name. The second project to be created will silently share data
with, or overwrite, the first. There is no check before write.

## Move

### 6. `~/.claude.json` is read, re-marshaled, and rewritten as a whole on every run

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

### 7. The per-project block in `~/.claude.json` is re-keyed but its contents are not rewritten

`internal/rewrite/rewrite.go:UserConfig` deletes the old key and inserts the
preserved `json.RawMessage` value under the new key. Embedded paths inside
the value — e.g. `mcpServers.*.args`, `mcpServers.*.env.*`, `mcpContextUris`,
`exampleFiles` — still point at the old project after a move.

### 8. `~/.claude/file-history/<session-uuid>/<hash>@vN` snapshots are never rewritten

`internal/move/move.go:Apply` rewrites sessions-index, transcripts, memory,
history, session files, settings, and config. It does not call any rewriter
on file-history snapshot contents. Snapshots that captured file contents
containing the old project path remain stale.

### 9. Session-subdir transcripts are not rewritten

`internal/move/move.go:collectNewDirTranscripts` reads only top-level `.jsonl`
files in the project dir. Transcripts stored under
`<uuid>/subagents/agent-*.jsonl` and any file under `<uuid>/session-memory/`
are not path-rewritten when `RewriteTranscripts: true`.

### 10. Rules files are scanned, never rewritten

`internal/scan/rules.go:Rules` reports occurrences of the old path as
`Warning`s. `move.Apply` does not modify anything under `~/.claude/rules/`.
Rules that hard-code the old project path require manual editing after a
move.

### 11. A path immediately followed by `.` is left untouched

`internal/rewrite/rewrite.go:ReplacePathInBytes` treats `[A-Za-z0-9_.-]` as
path-component bytes. This protects prefix collisions like `myproject` vs
`myproject.v2`. The trade-off: prose such as `"look at
/Users/x/myproject."` is not rewritten — the trailing `.` is
indistinguishable from the start of an extension.

### 12. Malformed history lines are preserved silently

`internal/rewrite/rewrite.go:HistoryJSONL` keeps malformed lines verbatim and
continues. The user receives no warning that those lines exist or that the
rewriter could not touch them.

### 13. `cwd` rewrite requires the old path as a strict prefix

`internal/rewrite/rewrite.go:SessionFile` calls `strings.HasPrefix`. A
`session.cwd` value that holds the project path in any other position (e.g.
inside a JSON-encoded payload) will not be rewritten.

## Export

### 14. History is filtered by exact `project` field equality

`internal/export/export.go:extractProjectHistory` only includes lines whose
`project` field equals the requested project path. History entries that
reference the project only in `display` or `pastedContents` are excluded
from the export.

### 15. Binary detection uses a 512-byte null-byte heuristic

`internal/export/export.go:isLikelyText` checks only the first 512 bytes for
a `\x00` byte. Files that are binary after a textual header, or binary
formats that happen to start with non-null bytes, are treated as text and
substring-rewritten — which corrupts them.

### 16. The export anonymizer is not path-boundary aware

`internal/export/export.go:anonymize` uses `strings.ReplaceAll`. It is
currently safe only because placeholders are sorted by `Original` length
descending, which handles the common HOME-prefix-of-PROJECT case. Adding
placeholders whose `Original` strings are substrings of each other in any
other order can corrupt output.

## Import

### 17. Import has no atomic or rollback guarantee

`internal/importer/importer.go:Run` streams ZIP entries and writes each to
its final destination as it goes: files into the encoded project directory,
appends to `~/.claude/history.jsonl`, an in-place rewrite of
`~/.claude.json`. A failure midway — out of disk, permission error, a
corrupt entry, a missing resolution — leaves some destinations written and
others not, with no equivalent of `move.Apply`'s copy-verify-delete
strategy. Rolling back a partial import is manual.

### 18. The archive `version` attribute is parsed but never validated

`internal/export/manifest.go` declares and writes `<cc-port version="1">`,
and `export.ReadManifestFromZip` unmarshals it into `Metadata.Version`. In
`internal/importer/importer.go:Run` the manifest is read purely for
structural validation (`if _, err := export.ReadManifestFromZip(...)`) —
the returned value is discarded. An archive claiming any `version` value is
accepted as long as the XML structure is valid, so a future cc-port format
change cannot be signaled through the version attribute without code
changes.

### 19. Unsupplied placeholders survive import as literal strings

`internal/importer/importer.go:Run` only resolves placeholders the caller
provided in `Options.Resolutions`. If the archive's `metadata.xml` declares
a placeholder the caller did not supply, the literal `{{KEY}}` string
remains in every imported file — there is no validation gate.

## Sessions-index

### 20. Real installations do not maintain `sessions-index.json`

The tool reads `sessions-index.json` for session metadata (`firstPrompt`,
`summary`, `gitBranch`, `messageCount`). Production installations of Claude
Code do not appear to write this file; `LocateProject` falls back to
filename-based UUID discovery, but the metadata fields are then unavailable
and the export carries no equivalent index entry.
