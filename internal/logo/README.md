# internal/logo

## Purpose

Renders the cc-port gantry-crane logo as colored ASCII for `--help`, `--version`, and interactive prompts. The art is a compact (22×14 rune) stylization of `docs/images/logo.png`; navy body, orange cable and trolley, one container hanging from the cable and two stacked on the ground between the legs. Navy comes from one of two palettes picked at runtime based on the terminal background.

## Public API

- `Render(w io.Writer) error`: writes the logo plus a trailing blank line when `w` is a terminal file; colored when `NO_COLOR` is unset, plain otherwise. Produces nothing when `w` is a pipe, file, or `bytes.Buffer`, so scripts consuming `cc-port --version` stay clean. Used by the interactive-prompt banner where there is no adjacent text to pair with.
- `RenderBeside(w io.Writer, text string) error`: writes the logo and the caller's text side by side. Text is vertically centered when it fits inside the logo's row count; longer text flows downward with the text column kept at a constant indent. On terminals narrower than the side-by-side threshold (logo + gutter + 50 cols), falls back to stacked (logo, blank line, text). On non-terminal writers, writes only the text.
- `BesideString(text string) string`: mirror of `RenderBeside` for cobra template funcs that cannot pass a writer. Gates on `os.Stdout` to match where cobra ultimately writes.
- `String(darkBackground, colored bool) string`: returns the logo as a string ending in `"\n\n"`. Callers own both decisions; used by tests and narrow-terminal fallback inside `BesideString`.
- `IsTerminal(w io.Writer) bool`: reports whether `w` is an `*os.File` attached to a terminal.
- `ColorEnabled() bool`: reports whether ANSI escapes should be emitted, honoring `NO_COLOR`.
- `HasDarkBackground() bool`: reports whether the terminal has a dark background, caching the answer for the process. One OSC 11 query via `lipgloss.HasDarkBackground` on first call; terminals that don't reply default to dark.

## Contracts

### No ANSI escapes leak into non-terminal writers

`Render` checks that the writer is an `*os.File` attached to a terminal before emitting anything. `NO_COLOR` suppresses escapes but keeps the ASCII body when the writer is a terminal. A `bytes.Buffer` or redirected file receives nothing at all, so piped `--version` output stays machine-parseable.

### Side-by-side layout

`RenderBeside` and `BesideString` place the caller's text to the right of the logo. Both logo and text center vertically inside the taller of the two: short text (fewer rows than the logo's 14) sits in the middle of the logo; long text (cobra `--help`) surrounds the logo symmetrically above and below, keeping the text column at a stable left indent end-to-end. Narrow terminals (width below `rowWidth + gutter + 50`, currently 75 cols) fall back to stacked: logo first, then a blank line, then text. A width of 0 from `term.GetSize` (unsized PTY, some CI containers) is treated as unknown and assumed to be the fallback width of 80 cols, so the layout doesn't collapse just because the PTY wasn't sized.

### Background-aware palette

`HasDarkBackground` runs one OSC 11 query via `lipgloss.HasDarkBackground(os.Stdin, os.Stdout)` on first call and caches the answer. When stdin is not a terminal (rare for the surfaces this package serves) the detection is skipped and the cache falls back to dark, because dark is the dominant default across iTerm, Terminal.app, Ghostty, and Windows Terminal. The light palette only fires on terminals that explicitly reply with a light background.

#### Handled

- Terminal stdout, `NO_COLOR` unset: colored ASCII.
- Terminal stdout, `NO_COLOR` set: plain ASCII, no escapes.
- Non-file writer (`bytes.Buffer`, `strings.Builder`, pipe `*os.File` with no TTY): nothing written.

#### Refused

None. The package does not error on "logo would be unreadable" conditions; callers that need the logo always call `Render`, and misuse is cosmetic only.

#### Not covered

- `FORCE_COLOR` or equivalent overrides. A caller that pipes the output but still wants color must build the string via `String(darkBackground, true)` and write it directly.
- Terminals narrower than 22 columns. The logo row is a fixed 22 runes; terminals under that width wrap each logo row, which degrades gracefully. Side-by-side already falls back to stacked below 75 columns, so this only bites in the extreme-narrow case.

## Tests

`logo_test.go` guards the width invariant (every row is exactly 22 runes), the colored/plain split, the non-terminal suppression path, and `composeBeside` layout (short text vertically centered inside the logo; long text wrapping the logo symmetrically above and below). The width test is the primary regression guard: a sloppy edit that breaks alignment fails here rather than in a user's terminal. Cobra-wiring tests in `cmd/cc-port/main_test.go` exercise the `--help`, `--version`, and `version` surfaces end-to-end via in-memory buffers; under `go test` the writer is not a terminal, so those tests cover the text-only path and leave side-by-side layout to the `composeBeside` unit tests.
