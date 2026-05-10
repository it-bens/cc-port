//go:build logo

package main

import "github.com/it-bens/cc-port/internal/logo"

// bannerImpl is the build-tag-selected banner implementation for the
// cc-port-with-logo binary (built with `go build -tags logo`). Renders the
// gantry-crane logo on --help, --version, the version subcommand, and
// the interactive prompt banner.
var bannerImpl = logo.Banner{}
