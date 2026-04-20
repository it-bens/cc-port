# internal/scan — agent notes

## Before editing

- Never open a rules file for writing. (README §Rules files preserved)
- Scan only `.md` files directly inside the rules directory; no recursion. (README §Rules files preserved)
- One `Warning` per matched line, not per matched path. (README §Rules files preserved)

## Navigation

- Entry: `rules.go:Rules`.
- Warning type: `rules.go:Warning`.
- Tests: `rules_test.go`.
