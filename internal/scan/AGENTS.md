# internal/scan — agent notes

Read-only scanner for `~/.claude/rules/*.md`. See `README.md` for the full contract.

## Before editing

- Never open a rules file for writing — this package is read-only by contract; any future rewrite support must be a new package (README §Rules files preserved).
- Only scan `.md` files directly inside the rules directory; do not recurse into subdirectories (README §Rules files preserved §Not covered).
- One `Warning` per matched line, not per matched path — callers rely on line-count correctness for summary output (README §Rules files preserved).

## Navigation

- Entry: `rules.go:Rules`.
- Warning type: `rules.go:Warning`.
- Tests: `rules_test.go`.

Read `README.md` before changing anything under `## Contracts`.
