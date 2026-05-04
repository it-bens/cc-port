# internal/ui — agent notes

## Before editing

- Call `requireTTY` at the top of every new prompt, before constructing a `huh` form (README §Interactive prompts require a TTY).
- Write surface-specific refusal messages that name the non-interactive alternative for each entry point's flow (README §Interactive prompts require a TTY).
- Do not extend the preflight to stdout or stderr; `huh` writes to `/dev/tty` directly (README §Interactive prompts require a TTY).
- Call `showInteractiveBanner` after `requireTTY` returns nil and before constructing any form; the sync.Once inside makes multi-prompt flows render the logo exactly once per process (README §Interactive banner).

## Navigation

- Entry: `prompt.go:SelectCategories`.
- Preflight: `prompt.go:requireTTY`.
- Banner: `prompt.go:showInteractiveBanner`.
- Tests: `prompt_test.go` (README §Tests).
