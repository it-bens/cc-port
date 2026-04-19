# internal/ui — agent notes

Interactive terminal prompts. See `README.md` for the full contract.

## Before editing

- Every new prompt must call `requireTTY` before constructing a `huh` form — non-TTY stdin must fail fast with a typed error, not reach `Run()` (README §Interactive prompts require a TTY).
- Refusal messages are surface-specific — each entry point names the non-interactive alternative that applies to its flow (category flags, manifest flow, `--resolution`) (README §Interactive prompts require a TTY).
- Do not extend the preflight to stdout/stderr — `huh` writes to `/dev/tty` directly and redirected streams are already handled (README §Interactive prompts require a TTY §Not covered).

## Navigation

- Entry: `prompt.go:SelectCategories`, `prompt.go:ResolvePlaceholder`.
- Preflight: `prompt.go:requireTTY`.
- Tests: no dedicated test — exercised via `integration_test.go` at repo root.

Read `README.md` before changing anything under `## Contracts`.
