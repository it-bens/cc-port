# internal/scan

## Purpose

Read-only scanner for `~/.claude/rules/*.md`.

## Public API

- `Rules(rulesDir string, paths ...string) ([]Warning, error)`: scans every `.md` directly inside `rulesDir` for occurrences of any given path and returns per-line `Warning` entries.
- `Warning`: struct with fields `File` (base filename, not full path), `Line` (1-based), `Text` (full line content), and `Path` (the search path that matched).

## Contracts

### Rules files preserved

Called by `internal/tool/claude` on three surfaces: `Export`, import `Finalize`, and move `ResidualWarnings`. See `internal/tool/claude/README.md` §Anonymisation (export) for the surrounding export flow.

#### Handled

`cc-port export` surfaces matches so the user can edit them manually before sharing an archive. `internal/tool/claude`'s `Export` runs `internal/scan/rules.go:Rules` over every `.md` file in `~/.claude/rules/`. Each line that contains the project path is reported as a `Warning` alongside the export's other warnings. Import's `Finalize` and move's `ResidualWarnings` run the same scan against their project path, so an import or move reports lingering rules references the same way.

Files on disk are not modified. One `Warning` is emitted per matched line, not per matched path.

Rules lines up to `maxScannerLine` (16 MiB) are scanned intact. Lines above that return `bufio.ErrTooLong` rather than being silently truncated. The scan fails hard so the user can inspect the oversized file by hand.

#### Refused

This package never opens a rules file for writing. A missing rules directory returns `(nil, nil)`. Non-`.md` entries and subdirectory entries are skipped silently.

#### Not covered

Only the top-level `.md` files in the rules directory are scanned. Nested subdirectories, non-`.md` extensions, and rules kept anywhere else on the system are ignored.

There is no `--rewrite-rules` flag. The warning is the entire remediation surface. The user is expected to inspect each hit and decide whether editing, leaving it, or moving the rule into the project is the right call.

## Tests

Unit tests in `rules_test.go`. Coverage:

- single-file match.
- multiple paths across multiple files.
- one warning per line even when multiple paths match.
- no-match case, empty directory, missing directory, non-`.md` files ignored.
- an unreadable rules directory propagates its permission error.
- 1 MiB line scanned intact, staying under the 16 MiB cap. A 17 MiB line returns `bufio.ErrTooLong`.

## References

- `bufio.Scanner.Buffer`: local authoritative: `go doc bufio.Scanner.Buffer` · online supplement: https://pkg.go.dev/bufio#Scanner.Buffer
