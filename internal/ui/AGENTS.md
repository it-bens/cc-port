# internal/ui — agent notes

## Before editing

- Call `requireTTY` at the top of every new prompt, before constructing a `huh` form (README §Interactive prompts require a TTY).
- Write surface-specific refusal messages that name the non-interactive alternative for each entry point's flow (README §Interactive prompts require a TTY).
- Do not extend the preflight to stdout or stderr; `huh` writes to `/dev/tty` directly (README §Interactive prompts require a TTY).
- Call `showInteractiveBanner(banner)` after `requireTTY` returns nil and before constructing any form; the sync.Once inside makes multi-prompt flows call `banner.Render` exactly once per process. The banner reaches the function as a `Banner` parameter from `SelectCategories`; never reach for an `internal/logo` symbol directly (README §Interactive banner).

## Navigation

- Entry: `prompt.go:SelectCategories`.
- Preflight: `prompt.go:requireTTY`.
- Banner: `prompt.go:showInteractiveBanner`.
- Tests: `prompt_test.go` (README §Tests).
