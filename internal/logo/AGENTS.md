# internal/logo — agent notes

## Before editing

- Keep every row in `art` exactly `rowWidth` runes wide once segment texts concatenate (README §No ANSI escapes leak into non-terminal writers).
- Never emit ANSI escapes from `Render` when the writer is not an `*os.File` attached to a terminal, even if `NO_COLOR` is unset (README §No ANSI escapes leak into non-terminal writers).
- Orange is load-bearing: the trolley and cable are the only orange segments and must map to the navy container-hook `┴` on the row below (README §Purpose).
- Detection of the terminal background goes through `HasDarkBackground` and the `sync.Once` cache, never through direct `lipgloss` or OSC queries at the call site (README §Background-aware palette).
- Treat a zero width from `term.GetSize` as unknown (fall back to 80), never as "narrow terminal"; unsized PTYs (macOS `script`, some CI) legitimately report zero (README §Side-by-side layout).

## Navigation

- Art grid: `logo.go:art`.
- Writer gates: `logo.go:Render`, `logo.go:RenderBeside`, `logo.go:IsTerminal`, `logo.go:terminalWidth`.
- Template entry points: `logo.go:String`, `logo.go:BesideString`, `logo.go:ColorEnabled`, `logo.go:HasDarkBackground`.
- Layout: `logo.go:composeBeside`.
- Tests: `logo_test.go`.
