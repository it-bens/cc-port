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

// noopBanner is the default banner used by the cc-port binary. Render
// writes nothing; RenderBeside writes the supplied text; BesideString
// returns the text unchanged. Satisfies both ui.Banner and the local
// Banner interface.
type noopBanner struct{}

func (noopBanner) Render(io.Writer) error { return nil }

func (noopBanner) RenderBeside(out io.Writer, text string) error {
	_, err := io.WriteString(out, text)
	return err
}

func (noopBanner) BesideString(_ io.Writer, text string) string {
	return text
}
