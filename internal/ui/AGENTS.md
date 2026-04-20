# internal/ui — agent notes

## Before editing

- Call `requireTTY` at the top of every new prompt, before constructing a `huh` form (README §Interactive prompts require a TTY).
- Write surface-specific refusal messages that name the non-interactive alternative for each entry point's flow (README §Interactive prompts require a TTY).
- Do not extend the preflight to stdout or stderr; `huh` writes to `/dev/tty` directly (README §Interactive prompts require a TTY).

## Navigation

- Entry: `prompt.go:SelectCategories`, `prompt.go:ResolvePlaceholder`.
- Preflight: `prompt.go:requireTTY`.
- Tests: no dedicated test; exercised via `integration_test.go` at repo root.
