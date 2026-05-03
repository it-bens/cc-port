package main

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/it-bens/cc-port/internal/scan"
)

// renderRulesReport writes a scan.Report to w. An empty report produces
// no output; an Err writes the "could not scan" line; a populated
// Warnings slice writes the canonical heading plus one indented line per
// warning. Used by every cmd that surfaces rules-file warnings.
//
// prefix is prepended to the heading line and indents the warning lines.
// Move passes "  └ " to keep its dry-run tree-glyph alignment; export,
// import, push, and pull pass "" for the bare two-space indent.
func renderRulesReport(w io.Writer, prefix string, report scan.Report) {
	if report.Err != nil {
		_, _ = fmt.Fprintf(w, "%sWarning: could not scan rules files: %v\n", prefix, report.Err)
		return
	}
	if len(report.Warnings) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "%sWarning: Rules files with matching paths:\n", prefix)
	// Count runes, not bytes: move passes "  └ " whose box-drawing glyph is
	// 3 bytes for 1 column, so len(prefix) over-indents by two spaces.
	indent := strings.Repeat(" ", utf8.RuneCountInString(prefix)) + "  "
	for _, warning := range report.Warnings {
		_, _ = fmt.Fprintf(w, "%s%s (line %d)\n", indent, warning.File, warning.Line)
	}
}
