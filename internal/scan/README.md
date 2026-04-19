# internal/scan

## Purpose

Read-only scanner for `~/.claude/rules/*.md`. Reports which lines reference a given project path so `move` can surface them to the user for manual review.

Not a rewriter — this module deliberately does not modify files on disk. Any callers who want rewriting should either edit by hand or wait for a future `--rewrite-rules` surface (not planned).

## Public API

- `Rules(rulesDir string, paths ...string) ([]Warning, error)` — scan every `.md` directly inside `rulesDir` for occurrences of any given path; return per-line `Warning` entries.
- `Warning` — struct with fields `File` (filename, not full path), `Line` (1-based), `Text` (full line content), and `Path` (the search path that matched).

## Contracts

### Rules files preserved

cc-port treats `~/.claude/rules/*.md` as user-scoped guidance that should
stay untouched by a project move. Rules live one directory up from any
single project; if a rule needs a project path, the rule belongs inside
the project (e.g. `CLAUDE.md` at the project root), not in the global
rules directory. An in-place rewrite under `~/.claude/rules/` would
silently edit content the user likely wants reviewed by hand.

Handled — `move` surfaces matches so the user can edit them manually:

- `cc-port move` (apply or dry-run) runs `internal/scan/rules.go:Rules`
  over every `.md` file in `~/.claude/rules/` and reports each line that
  contains the old project path as a `Warning` alongside the rest of the
  plan output. The files on disk are not modified; one `Warning` is
  emitted per matched line, not per matched path.
- Rules lines up to `maxScannerLine` (16 MiB) are scanned intact. Lines
  above that return `bufio.ErrTooLong` rather than being silently
  truncated — the scan surface fails-hard so the user can inspect the
  oversized file by hand.

Refused — nothing: this package is read-only by contract, so no inputs
are rejected. A missing rules directory returns `(nil, nil)`; a non-`.md`
or subdirectory entry is skipped silently.

Not covered — cases cc-port does not address:

- **Files outside `~/.claude/rules/`.** Only the top-level `.md` files in
  that directory are scanned. Nested subdirectories, non-`.md` extensions,
  and rules kept anywhere else on the system are ignored.
- **Automatic rewrite.** There is no `--rewrite-rules` flag. The warning
  is the entire remediation surface; the user is expected to inspect each
  hit and decide whether editing it, leaving it, or moving the rule into
  the project is the right call.

Called by `internal/move` (both `DryRun` and `Apply`) — see `internal/move/README.md` §Malformed history entries preserved for the surrounding plan/apply flow.

## Tests

Unit tests in `rules_test.go`. Coverage: single-file match, multiple paths across multiple files, one warning per line even when multiple paths match, no-match case, empty directory, missing directory, non-`.md` files ignored.

Additional tests cover the 16 MiB line cap: one line below the cap scans successfully, one line above the cap returns `bufio.ErrTooLong`.

## References

- `bufio.Scanner.Buffer` — local authoritative: `go doc bufio.Scanner.Buffer` · online supplement: https://pkg.go.dev/bufio#Scanner.Buffer
