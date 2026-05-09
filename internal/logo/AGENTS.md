# internal/logo — agent notes

## Before editing

- Keep every row in `art` exactly `rowWidth` runes wide once segment texts concatenate (README §No ANSI escapes leak into non-terminal writers).
- Never emit ANSI escapes from `Banner.Render` or `Banner.RenderBeside` when the writer is not an `*os.File` attached to a terminal, even if `NO_COLOR` is unset (README §No ANSI escapes leak into non-terminal writers).
- Orange is load-bearing: the trolley and cable are the only orange segments and must map to the navy container-hook `┴` on the row below (README §Purpose).
- Detection of the terminal background goes through the unexported `hasDarkBackground` and the `sync.Once` cache, never through direct `lipgloss` or OSC queries at the call site (README §Background-aware palette).
- Treat a zero width from `term.GetSize` as unknown (fall back to 80), never as "narrow terminal"; unsized PTYs (macOS `script`, some CI) legitimately report zero (README §Side-by-side layout).
- The package is opt-in via the `logo` build tag. Only `cmd/cc-port/banner_logo.go` imports it. Adding a new external import means changing the build-tag wiring at the composition root, not adding callers (README §Used by).

## Navigation

- Public API: `logo.go:Banner` and its three methods (`Render`, `RenderBeside`, `BesideString`).
- Art grid: `logo.go:art`.
- Helpers (unexported): `logo.go:render`, `logo.go:renderBeside`, `logo.go:composedString`, `logo.go:isTerminal`, `logo.go:terminalWidth`, `logo.go:colorEnabled`, `logo.go:hasDarkBackground`, `logo.go:composeBeside`.
- Tests: `logo_test.go`.
