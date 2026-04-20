# internal/ui

## Purpose

Interactive terminal prompts for the CLI's `export`, `export manifest`, and `import` surfaces. Backed by `charm.land/huh/v2` forms.

Not a general UI layer. The only prompts this package exposes are the two listed below, and callers with a non-interactive alternative should skip this package entirely.

## Public API

- **Prompt entry points**
  - `SelectCategories() (manifest.CategorySet, error)`: interactive category picker for `export` / `export manifest`.
  - `ResolvePlaceholder(key, original, autoValue string) (string, error)`: prompts for one manifest placeholder and returns the user's input verbatim. The prompt does not validate. `import` rejects an empty return value via `importer.ValidateResolutions` and `export` marks the placeholder `Resolvable: false`.

## Contracts

### Interactive prompts require a TTY

Callers: `export`, `export manifest`, `import` (via `internal/export` and `internal/importer`).

#### Handled

`prompt.go:requireTTY` runs at the top of every prompt entry point. It checks `term.IsTerminal(os.Stdin.Fd())` before any form is constructed. On non-TTY stdin the function returns a typed error naming the missing input and the non-interactive alternative for that surface. The terminal state is never perturbed.

These invocations satisfy the invariant because no form runs at all:

- Any category-flag combination on `export` / `export manifest`. The interactive picker is skipped in `resolveExportCategories` before `SelectCategories` is reached.
- `export --from-manifest` and `import --from-manifest`. The manifest already carries every category and every placeholder resolution.
- `import` of an archive whose every declared placeholder is either `{{PROJECT_PATH}}` (resolved implicitly from the target path), pre-supplied via `--resolution KEY=VALUE`, or already carries a non-empty `<resolve>` in the manifest.
- Any invocation with stdin attached to a real terminal. `requireTTY` returns nil and the form runs unchanged.

#### Refused

Non-TTY stdin triggers the preflight abort with a surface-specific remediation message before any form is constructed:

- `SelectCategories` (interactive category picker for `export` / `export manifest`): refused with a pointer to `--all` or the per-category flags (`--sessions`, `--memory`, `--history`, `--file-history`, `--config`, `--todos`, `--usage-data`, `--plugins-data`, `--tasks`).
- `ResolvePlaceholder` (used by `export` for non-auto-detected path suggestions and by `import` for manifest keys with no pre-filled `<resolve>`): refused with a pointer to the two-step manifest flow (`export manifest` / `import --from-manifest`) or, on `import`, `--resolution KEY=VALUE`.

#### Not covered

- **Stdin-only detection.** The check looks at `os.Stdin.Fd()` only. When stdin is a TTY but stdout or stderr is redirected (`cc-port export ... | tee log`), the invocation is classified as interactive and the form runs normally (`huh` writes directly to `/dev/tty`). When stdin is a TTY but `huh` cannot open the controlling terminal (detached session, unusual sandbox), the form runs and fails inside `Run()`. The preflight cannot detect that class.
- **Concurrent stdin closure.** A process that closes the preflighted stdin between `requireTTY` returning nil and the form opening the TTY is not re-checked. This is a race a caller would have to deliberately engineer. Normal shell and CI environments do not produce it.

## Tests

No dedicated `prompt_test.go`. The `huh` forms take over `/dev/tty` and are not exercised under `go test`. The `requireTTY` preflight is exercised indirectly by `integration_test.go` at the repo root.
