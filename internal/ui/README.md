# internal/ui

## Purpose

Interactive terminal prompts for the CLI's `export` and `export manifest` surfaces. Backed by `charm.land/huh/v2` forms.

Not a general UI layer. The only prompt this package exposes is the one listed below, and callers with a non-interactive alternative should skip this package entirely.

## Public API

- **Prompt entry points**
  - `SelectCategories(banner Banner) (manifest.CategorySet, error)`: interactive multi-select category picker for `export` / `export manifest`. Iterates `manifest.AllCategories`, presents each with the description from the unexported `categoryOptionMeta` table, and folds the user's selection into a `manifest.CategorySet` via `manifest.SpecByName`. The `banner` parameter is the consumer-defined interface satisfied by the build-tag-selected implementation in `cmd/cc-port`.

## Contracts

### Interactive banner

`SelectCategories` calls `showInteractiveBanner(banner)` after `requireTTY` returns nil and before constructing the form. The injected `Banner` (one method, `Render(io.Writer) error`) writes to `os.Stdout` directly behind a package-scoped `sync.Once`, so a process that prompts more than once still calls `banner.Render` at most once. The banner is cosmetic; the real impl in the cc-port-logo build self-suppresses on non-terminal writers and under `NO_COLOR`, and the no-op impl in the default build writes nothing.

### Banner is consumer-defined

`Banner` is declared in `prompt.go` as a one-method interface (`Render(io.Writer) error`). It lives next to its sole consumer `showInteractiveBanner`; the implementation is supplied by `cmd/cc-port` (see `cmd/cc-port/README.md` §Banner DI). The package never imports `internal/logo`.

### Interactive prompts require a TTY

Callers: `export`, `export manifest` (via `internal/export`).

#### Handled

`prompt.go:requireTTY` runs at the top of `SelectCategories`. It checks `term.IsTerminal(os.Stdin.Fd())` before any form is constructed. On non-TTY stdin the function returns a typed error naming the missing input and the non-interactive alternative. The terminal state is never perturbed.

These invocations satisfy the invariant because no form runs at all:

- Any category-flag combination on `export` / `export manifest`. The interactive picker is skipped in `resolveExportCategories` before `SelectCategories` is reached.
- `export --from-manifest`. The manifest already carries every category.
- Any invocation with stdin attached to a real terminal. `requireTTY` returns nil and the form runs unchanged.

#### Refused

Non-TTY stdin triggers the preflight abort with a remediation message before any form is constructed:

- `SelectCategories` (interactive category picker for `export` / `export manifest`): refused with a pointer to `--all` or the per-category flags (`--sessions`, `--memory`, `--history`, `--file-history`, `--config`, `--todos`, `--usage-data`, `--plugins-data`, `--tasks`).

#### Not covered

- **Stdin-only detection.** The check looks at `os.Stdin.Fd()` only. When stdin is a TTY but stdout or stderr is redirected (`cc-port export ... | tee log`), the invocation is classified as interactive and the form runs normally (`huh` writes directly to `/dev/tty`). When stdin is a TTY but `huh` cannot open the controlling terminal (detached session, unusual sandbox), the form runs and fails inside `Run()`. The preflight cannot detect that class.
- **Concurrent stdin closure.** A process that closes the preflighted stdin between `requireTTY` returning nil and the form opening the TTY is not re-checked. This is a race a caller would have to deliberately engineer. Normal shell and CI environments do not produce it.

## Tests

`prompt_test.go` covers the TTY preflight path and the runner-error path for `SelectCategories`, plus every entry of `manifest.AllCategories` through `categoriesFromSelections`. A drift-guard test asserts every `manifest.AllCategories` entry has UI option metadata in the unexported `categoryOptionMeta` map. Tests can override three package-level seams (`isTerminal`, `runForm`, `interactiveBannerOnce`) to bypass the terminal requirement, the `huh` event loop, and the once-guard that would otherwise silence the banner path after the first test runs it. The path where the user submits a real selection is not exercised under `go test`, and runs only when the CLI is driven interactively.
