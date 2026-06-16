package progress

import "os"

// Selection is the flag-derived intent a command passes to Pick. Output is the
// sink the chosen renderer writes to (real os.Stderr in production); its
// terminal-ness is probed through the isTTY seam.
type Selection struct {
	JSON    bool
	Quiet   bool
	Verbose bool
	Debug   bool
	Output  *os.File
}

// Pick maps a Selection to a concrete Renderer and the active Level. Renderer
// choice: --json wins over everything (even a TTY); then --quiet; then a TTY
// sink gets the Ledger; otherwise the Stream. Level is independent of renderer:
// --quiet pins LevelError, else --debug, --verbose, default LevelInfo.
func Pick(selection Selection) (Renderer, Level) {
	return pickRenderer(selection), pickLevel(selection)
}

func pickRenderer(selection Selection) Renderer {
	switch {
	case selection.JSON:
		return NewJSONRenderer(selection.Output)
	case selection.Quiet:
		return NewNullRenderer(selection.Output)
	case isTTY(selection.Output):
		return NewLedgerRenderer(selection.Output)
	default:
		return NewStreamRenderer(selection.Output)
	}
}

func pickLevel(selection Selection) Level {
	switch {
	case selection.Quiet:
		return LevelError
	case selection.Debug:
		return LevelDebug
	case selection.Verbose:
		return LevelVerbose
	default:
		return LevelInfo
	}
}
