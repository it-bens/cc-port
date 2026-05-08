//go:build !logo

package main

// bannerImpl is the build-tag-selected banner implementation read by
// main(). The default cc-port binary uses noopBanner; the cc-port-logo
// binary (built with -tags logo) overrides this in banner_logo.go.
//
// This is the unexported package-level seam pattern allowed by
// docs/design-rules.md §"Plug an injectable dependency into a
// function": the dependency reaches consumers via function parameters
// (newRootCmd, ui.SelectCategories), and bannerImpl is just where the
// build-tag selection lands so main() can read it once.
var bannerImpl = noopBanner{}
