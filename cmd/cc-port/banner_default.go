//go:build !logo

package main

import "io"

// bannerImpl is the build-tag-selected banner implementation read by
// main(). The default cc-port binary uses noopBanner; the cc-port-with-logo
// binary (built with -tags logo) overrides this in banner_logo.go.
//
// This is the unexported package-level seam pattern allowed by
// docs/design-rules.md §"Plug an injectable dependency into a
// function": the dependency reaches consumers via function parameters
// (newRootCmd, ui.SelectCategories), and bannerImpl is just where the
// build-tag selection lands so main() can read it once.
var bannerImpl = noopBanner{}

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
