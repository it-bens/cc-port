package main

import (
	"io"

	"github.com/it-bens/cc-port/internal/ui"
)

// Banner is the consumer-defined interface for cobra help and version
// surfaces. It embeds ui.Banner so the same banner value can be passed
// to ui.SelectCategories without losing the Render method through
// interface narrowing.
type Banner interface {
	ui.Banner
	RenderBeside(out io.Writer, text string) error
	BesideString(out io.Writer, text string) string
}
