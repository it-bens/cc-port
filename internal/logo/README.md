# internal/logo

## Purpose

Renders the cc-port gantry-crane logo as colored ASCII for the cc-port-logo binary's `--help`, `--version`, and interactive prompts. The art is a compact (22×14 rune) stylization of `docs/images/logo.png`; navy body, orange cable and trolley, one container hanging from the cable and two stacked on the ground between the legs. Navy comes from one of two palettes picked at runtime based on the terminal background.

The package is opt-in via the `logo` build tag and is compiled into the cc-port-logo binary only. The default cc-port binary does not import this package.

## Public API

- `type Banner struct{}`: the sole public symbol. Zero-value struct; no constructor needed. Three methods:
  - `(Banner).Render(w io.Writer) error` writes the logo plus a trailing blank line when `w` is a terminal file with `NO_COLOR` controlling color. Writes nothing when `w` is a pipe, file, or `bytes.Buffer`. Used by the interactive-prompt banner.
  - `(Banner).RenderBeside(w io.Writer, text string) error` writes the logo and the caller's text side by side. Vertically centers when text fits inside the logo's row count; long text wraps the logo symmetrically. Falls back to stacked layout on terminals narrower than the side-by-side threshold (logo + gutter + 50 cols). On non-terminal writers, writes only the text.
  - `(Banner).BesideString(out io.Writer, text string) string` mirror of `RenderBeside` for cobra version templates that cannot pass a writer through to a Render call. The `out` parameter is used only to gate terminal-vs-text behavior; nothing is written to it.

## Contracts

### No ANSI escapes leak into non-terminal writers

`Banner.Render` and `Banner.RenderBeside` check that the writer is an `*os.File` attached to a terminal before emitting any color. `NO_COLOR` suppresses escapes but keeps the ASCII body when the writer is a terminal. A `bytes.Buffer` or redirected file receives nothing from `Render` and only the supplied text from `RenderBeside`, so piped `--version` output stays machine-parseable.

### Side-by-side layout

`Banner.RenderBeside` and `Banner.BesideString` place the caller's text to the right of the logo. Both logo and text center vertically inside the taller of the two. Narrow terminals (width below `rowWidth + gutter + 50`, currently 75 cols) fall back to stacked: logo first, then a blank line, then text. A width of 0 from `term.GetSize` (unsized PTY, some CI containers) is treated as unknown and assumed to be the fallback width of 80 cols.

### Background-aware palette

`hasDarkBackground` runs one OSC 11 query via `lipgloss.HasDarkBackground(os.Stdin, os.Stdout)` on first call and caches the answer. When stdin is not a terminal the detection is skipped and the cache falls back to dark, because dark is the dominant default across iTerm, Terminal.app, Ghostty, and Windows Terminal. The light palette only fires on terminals that explicitly reply with a light background.

#### Handled

- Terminal stdout, `NO_COLOR` unset: colored ASCII.
- Terminal stdout, `NO_COLOR` set: plain ASCII, no escapes.
- Non-file writer (`bytes.Buffer`, `strings.Builder`, pipe `*os.File` with no TTY): nothing written for `Render`, text-only for `RenderBeside`, text returned unchanged for `BesideString`.

#### Refused

None. The package does not error on "logo would be unreadable" conditions; misuse is cosmetic only.

#### Not covered

- `FORCE_COLOR` or equivalent overrides.
- Terminals narrower than 22 columns. The logo row is a fixed 22 runes; terminals under that width wrap each logo row, which degrades gracefully.

## Used by

`cmd/cc-port/banner_logo.go` is the sole external file that imports `internal/logo`. That file is gated by `//go:build logo` and only compiles into the cc-port-logo binary.

## Tests

`logo_test.go` guards the width invariant (every row is exactly 22 runes), the colored/plain split, the non-terminal suppression path, and `composeBeside` layout. The width test is the primary regression guard. Cobra-wiring tests in `cmd/cc-port/main_test.go` exercise the `--help`, `--version`, and `version` surfaces end-to-end via in-memory buffers; under `go test` (default tags) they run with `noopBanner{}` and cover the text-only path.
