# internal/ui

## Purpose

Interactive terminal prompts used by the CLI's `export`, `export manifest`, and `import` surfaces. Backed by `charm.land/huh/v2` forms.

Not a general UI layer — the only prompts this package exposes are the two listed below, and callers with a non-interactive alternative should skip this package entirely.

## Public API

- **Prompt entry points**
  - `SelectCategories() (manifest.CategorySet, error)` — interactive category picker for `export` / `export manifest`.
  - `ResolvePlaceholder(key, original, autoValue string) (string, error)` — prompts for one manifest placeholder and returns the user's input verbatim. The prompt does not validate; callers interpret the empty string (`import` rejects it via `importer.ValidateResolutions`; `export` marks the placeholder `Resolvable: false`).

## Contracts

### Interactive prompts require a TTY

cc-port's prompt surface lives in `internal/ui/prompt.go` and uses
`charm.land/huh/v2` forms for category selection (`SelectCategories`)
and placeholder resolution (`ResolvePlaceholder`). Each form takes over
the terminal when `Run()` is called, so an invocation without a
controlling TTY — piping into another process, a CI job, a shell script
without a `tty` allocation — would either block on input it will never
receive or surface huh's opaque `open /dev/tty: device not configured`
failure after the form has already grabbed the screen.

`internal/ui/prompt.go:requireTTY` runs at the top of every prompt entry
point and checks `term.IsTerminal(os.Stdin.Fd())` before any form is
constructed. On non-TTY stdin the function returns a typed error naming
the missing input and the non-interactive alternative for that surface,
so the failure is actionable and the terminal state is never perturbed.

Refused before any form runs — the preflight aborts with a surface-specific
remediation message:

- `SelectCategories` (interactive category picker for `export` /
  `export manifest`): refused with a pointer to `--all` or the
  per-category flags (`--sessions`, `--memory`, `--history`,
  `--file-history`, `--config`, `--todos`, `--usage-data`,
  `--plugins-data`, `--tasks`).
- `ResolvePlaceholder` (used by `export` for non-auto-detected path
  suggestions and by `import` for manifest keys with no pre-filled
  `<resolve>`): refused with a pointer to the two-step manifest flow
  (`export manifest` / `import --from-manifest`) or, on `import`,
  `--resolution KEY=VALUE`.

Handled — invocations that never trip the preflight:

- Any category-flag combination on `export` / `export manifest`. The
  interactive picker is skipped in `resolveExportCategories` before
  `SelectCategories` is reached.
- `export --from-manifest` and `import --from-manifest`. The manifest
  already carries every category and every placeholder resolution, so
  neither form runs.
- `import` of an archive whose every declared placeholder is either
  `{{PROJECT_PATH}}` (resolved implicitly from the target path), pre-supplied
  via `--resolution KEY=VALUE`, or already carries a non-empty `<resolve>`
  in the manifest — no placeholder prompt is needed.
- Any invocation with stdin attached to a real terminal — `requireTTY`
  returns nil and the form runs unchanged.

Not covered — cases this guard deliberately does not address:

- **Stdin-only detection.** The check looks at `os.Stdin.Fd()` only. An
  invocation whose stdin is a TTY but whose stdout or stderr is
  redirected (`cc-port export … | tee log`) is classified as
  interactive and the form runs normally — huh writes directly to
  `/dev/tty` in that setup, which is the desired behaviour. Conversely,
  a setup where stdin is a TTY but huh still cannot open the
  controlling terminal (detached session, unusual sandbox) will reach
  the form and fail inside `Run()`; the preflight cannot detect that
  class.
- **Concurrent stdin closure.** A process that closes the preflighted
  stdin between `requireTTY` returning nil and the form opening the
  TTY is not re-checked. This is a race a caller would have to
  deliberately engineer; normal shell and CI environments do not
  produce it.

## Tests

No dedicated `prompt_test.go` — the `huh` forms take over `/dev/tty` and are not exercised under `go test`. The `requireTTY` preflight is exercised indirectly by `integration_test.go` at the repo root, which runs the CLI end-to-end.
