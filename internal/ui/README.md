# internal/ui

## Purpose

Interactive terminal prompts for the CLI's `export` and `export manifest`
surfaces, across every selected tool. Backed by `charm.land/huh/v2` forms.

Not a general UI layer. The only prompt this package exposes is the one
listed below, and callers with a non-interactive alternative should skip
this package entirely.

## Public API

- **Prompt entry points**
  - `SelectCategories(banner Banner, tools []tool.Tool) (map[string]map[string]bool, error)`:
    interactive multi-select category picker for `export` / `export
    manifest`, grouped by tool across every tool in `tools`. Each option's
    label comes directly from that tool's `Category.Description`; when more
    than one tool is present, the label is prefixed with
    `[<DisplayName>]` to disambiguate. Returns the selection as
    `tool name -> category name -> included`. The `banner` parameter is the
    consumer-defined interface satisfied by the build-tag-selected
    implementation in `cmd/cc-port`.

## Contracts

### Interactive banner

`SelectCategories` calls `showInteractiveBanner(banner)` after `requireTTY`
returns nil and before constructing the form. The injected `Banner` (one
method, `Render(io.Writer) error`) writes to `os.Stdout` directly behind a
package-scoped `sync.Once`, so a process that prompts more than once still
calls `banner.Render` at most once. The banner is cosmetic; the real impl in
the cc-port-with-logo build self-suppresses on non-terminal writers and
under `NO_COLOR`, and the no-op impl in the default build writes nothing.

### Banner is consumer-defined

`Banner` is declared in `prompt.go` as a one-method interface
(`Render(io.Writer) error`). It lives next to its sole consumer
`showInteractiveBanner`; the implementation is supplied by `cmd/cc-port`
(see `cmd/cc-port/README.md` §Banner DI). The package never imports
`internal/logo`.

### Interactive prompts require a TTY

Callers: `export`, `export manifest` (via the `cmd/cc-port` category
selection path).

#### Handled

`prompt.go:requireTTY` runs at the top of `SelectCategories`. It checks
`term.IsTerminal(os.Stdin.Fd())` before any form is constructed. On non-TTY
stdin the function returns a typed error naming the missing input and the
non-interactive alternative. The terminal state is never perturbed.

These invocations satisfy the invariant because no form runs at all:

- Any category-flag combination on `export` / `export manifest` (`--all` or
  `--include <tool>/<category>`). The interactive picker is skipped in
  `resolveSelectionFromCmd` before `SelectCategories` is reached.
- `export --from-manifest`. The manifest already carries every category.
- Any invocation with stdin attached to a real terminal. `requireTTY`
  returns nil and the form runs unchanged.

#### Refused

Non-TTY stdin triggers the preflight abort with a remediation message
before any form is constructed:

- `SelectCategories` (interactive category picker for `export` / `export
  manifest`): refused with a pointer to `--all` or
  `--include <tool>/<category>`.

#### Not covered

- **Stdin-only detection.** The check looks at `os.Stdin.Fd()` only. When
  stdin is a TTY but stdout or stderr is redirected
  (`cc-port export ... | tee log`), the invocation is classified as
  interactive and the form runs normally (`huh` writes directly to
  `/dev/tty`). When stdin is a TTY but `huh` cannot open the controlling
  terminal (detached session, unusual sandbox), the form runs and fails
  inside `Run()`. The preflight cannot detect that class.
- **Concurrent stdin closure.** A process that closes the preflighted stdin
  between `requireTTY` returning nil and the form opening the TTY is not
  re-checked. This is a race a caller would have to deliberately engineer.
  Normal shell and CI environments do not produce it.

## Tests

`prompt_test.go` covers the once-per-process banner guard, the TTY preflight
rejection (asserting the error names both `--all` and `--include`), and the
runner-error wrap, all against a single-tool `[]tool.Tool{claude.New()}`
input. Tests override three package-level seams (`isTerminal`, `runForm`,
`interactiveBannerOnce`) to bypass the terminal requirement, the `huh` event
loop, and the once-guard that would otherwise silence the banner path after
the first test runs it. The multi-tool grouped-label prefix
(`[<DisplayName>]`) and the path where the user submits a real selection are
not exercised under `go test`; the label logic runs only when more than one
tool is selected, and form submission runs only when the CLI is driven
interactively.
