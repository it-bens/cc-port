// Package logo renders the cc-port gantry-crane logo as colored ASCII.
package logo

import (
	"io"
	"os"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// 24-bit ANSI foreground escapes. The dark palette is the docs/images/logo.png
// brand navy. The light palette is a brighter blue so the logo stays readable
// on dark terminal backgrounds where the brand navy would sink into the bg.
const (
	ansiNavyOnLightBg = "\x1b[38;2;30;38;86m"
	ansiNavyOnDarkBg  = "\x1b[38;2;140;160;235m"
	ansiOrange        = "\x1b[38;2;255;107;61m"
	ansiReset         = "\x1b[0m"
)

type color uint8

const (
	colorNavy color = iota
	colorOrange
)

type segment struct {
	text  string
	color color
}

// art is the logo as a grid of colored segments. Every row is exactly
// rowWidth runes wide once segment texts are concatenated; width_test
// enforces the invariant so edits that break alignment fail loudly.
// The extra column over a naive design gives the container stack a true
// midline at col 11: trolley, cable, and container hook ┴ all align.
const (
	rowWidth        = 22
	containerRibRow = " │╳│    │║║║║║│    │╳│"
)

var art = [][]segment{
	{{" ┌───────────────────┐", colorNavy}},
	{{" │                   │", colorNavy}},
	{{" ├─┬─────┬───┬─────┬─┤", colorNavy}},
	{
		{" │█│     ", colorNavy},
		{"└───┘", colorOrange},
		{"     │█│", colorNavy},
	},
	{
		{" │╳│       ", colorNavy},
		{"│", colorOrange},
		{"       │╳│", colorNavy},
	},
	{{" │╳│    ┌──┴──┐    │╳│", colorNavy}},
	{{containerRibRow, colorNavy}},
	{{" │╳│    └─────┘    │╳│", colorNavy}},
	{{" │╳│               │╳│", colorNavy}},
	{{" │╳│    ┌─────┐    │╳│", colorNavy}},
	{{containerRibRow, colorNavy}},
	{{" │╳│    ├─────┤    │╳│", colorNavy}},
	{{containerRibRow, colorNavy}},
	{{" └─┘    └─────┘    └─┘", colorNavy}},
}

// Side-by-side layout: logo on the left, text on the right. The pad
// separates them by a fixed gutter. minSideBySideWidth is the smallest
// terminal width that still leaves ~50 columns for text after the logo
// and the gutter; narrower terminals fall back to stacked layout.
const (
	besidePad           = "   "
	minSideBySideWidth  = rowWidth + len(besidePad) + 50
	fallbackTermColumns = 80
)

// render writes the logo followed by a trailing blank line to w. Output
// carries ANSI color when w is a terminal file and NO_COLOR is unset;
// plain ASCII when w is a terminal but NO_COLOR suppresses color;
// nothing at all when w is not a terminal (pipe, file, bytes.Buffer).
// Navy is picked from the dark or light palette based on the detected
// terminal background.
func render(w io.Writer) error {
	if !isTerminal(w) {
		return nil
	}
	_, err := io.WriteString(w, composedString(hasDarkBackground(), colorEnabled()))
	return err
}

// renderBeside writes the logo and text side by side to w. Both logo
// and text center vertically inside the taller of the two: short text
// sits in the middle of the logo; long text surrounds the logo above
// and below at a stable left indent. On non-terminal writers, only
// text is written. On terminals narrower than minSideBySideWidth,
// falls back to stacked layout (logo, blank line, text).
func renderBeside(w io.Writer, text string) error {
	if !isTerminal(w) {
		_, err := io.WriteString(w, text)
		return err
	}
	if terminalWidth(w) < minSideBySideWidth {
		if err := render(w); err != nil {
			return err
		}
		_, err := io.WriteString(w, text)
		return err
	}
	_, err := io.WriteString(w, composeBeside(text, hasDarkBackground(), colorEnabled()))
	return err
}

func composeBeside(text string, darkBackground, colored bool) string {
	logoLines := strings.Split(strings.TrimRight(composedString(darkBackground, colored), "\n"), "\n")
	textLines := strings.Split(strings.TrimRight(text, "\n"), "\n")

	blankLogoRow := strings.Repeat(" ", rowWidth)

	// Both logo and text vertically center inside the taller of the two;
	// no hardcoded row count. Short text sits in the middle of the logo;
	// long text (cobra help) surrounds the logo symmetrically above and
	// below, keeping the text column stable end-to-end.
	totalRows := max(len(logoLines), len(textLines))
	logoStart := (totalRows - len(logoLines)) / 2
	textStart := (totalRows - len(textLines)) / 2

	var buffer strings.Builder
	for rowIndex := range totalRows {
		logoIndex := rowIndex - logoStart
		if logoIndex >= 0 && logoIndex < len(logoLines) {
			buffer.WriteString(logoLines[logoIndex])
		} else {
			buffer.WriteString(blankLogoRow)
		}
		buffer.WriteString(besidePad)
		textIndex := rowIndex - textStart
		if textIndex >= 0 && textIndex < len(textLines) {
			buffer.WriteString(textLines[textIndex])
		}
		buffer.WriteByte('\n')
	}
	return buffer.String()
}

func terminalWidth(w io.Writer) int {
	file, ok := w.(*os.File)
	if !ok {
		return fallbackTermColumns
	}
	width, _, err := term.GetSize(file.Fd())
	// A zero width means TIOCGWINSZ returned no size; treat as unknown.
	// Common under `script` on macOS, inside some CI containers, and
	// when the PTY hasn't been SIGWINCH-sized yet.
	if err != nil || width <= 0 {
		return fallbackTermColumns
	}
	return width
}

// composedString returns the logo as a single string ending in "\n\n".
// When colored is true, navy and orange segments are wrapped in ANSI
// foreground escapes; darkBackground picks the lighter navy palette so
// the logo stays readable on dark terminals.
func composedString(darkBackground, colored bool) string {
	var buffer strings.Builder
	buffer.Grow(len(art) * (rowWidth + 16))
	for _, row := range art {
		for _, seg := range row {
			if colored {
				buffer.WriteString(ansiCode(seg.color, darkBackground))
			}
			buffer.WriteString(seg.text)
			if colored {
				buffer.WriteString(ansiReset)
			}
		}
		buffer.WriteByte('\n')
	}
	buffer.WriteByte('\n')
	return buffer.String()
}

// isTerminal reports whether w is an *os.File attached to a terminal.
func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(file.Fd())
}

// colorEnabled reports whether ANSI escapes should be emitted. Honors
// the NO_COLOR convention (https://no-color.org/).
func colorEnabled() bool {
	return os.Getenv("NO_COLOR") == ""
}

// hasDarkBackground reports whether the terminal has a dark background,
// caching the answer for the process. Detection sends an OSC 11 query
// via lipgloss; on terminals that don't reply, lipgloss defaults to
// dark, which matches the dominant modern default.
func hasDarkBackground() bool {
	darkBackgroundOnce.Do(func() {
		if !term.IsTerminal(os.Stdin.Fd()) {
			darkBackground = true
			return
		}
		darkBackground = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	})
	return darkBackground
}

var (
	darkBackgroundOnce sync.Once
	darkBackground     bool
)

func ansiCode(c color, darkBackground bool) string {
	if c == colorOrange {
		return ansiOrange
	}
	if darkBackground {
		return ansiNavyOnDarkBg
	}
	return ansiNavyOnLightBg
}

// Banner is the public entry point for the gantry-crane logo. The zero
// value is ready to use; the type carries no state because all rendering
// inputs (terminal detection, color, dark-background) are queried from
// the writer or environment at render time.
type Banner struct{}

// Render writes the logo plus a trailing blank line to w when w is a
// terminal file with NO_COLOR unset (colored) or set (plain ASCII).
// Writes nothing when w is a pipe, file, or bytes.Buffer.
func (Banner) Render(w io.Writer) error {
	return render(w)
}

// RenderBeside writes the logo and text side by side to w. Falls back
// to stacked layout on narrow terminals; writes only text on non-
// terminal writers.
func (Banner) RenderBeside(w io.Writer, text string) error {
	return renderBeside(w, text)
}

// BesideString returns the side-by-side composition for callers that
// cannot pass a writer through to RenderBeside (e.g. cobra version
// templates). The out parameter is used only to gate terminal-vs-text
// behavior; nothing is written to it. On non-terminal writers, returns
// text unchanged.
func (Banner) BesideString(out io.Writer, text string) string {
	if !isTerminal(out) {
		return text
	}
	if terminalWidth(out) < minSideBySideWidth {
		return composedString(hasDarkBackground(), colorEnabled()) + text
	}
	return composeBeside(text, hasDarkBackground(), colorEnabled())
}
