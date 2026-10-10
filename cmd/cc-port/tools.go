package main

import (
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
	"github.com/it-bens/cc-port/internal/tool/codex"
)

// newToolSet is the composition root: the one place cc-port lists its
// supported tools.
func newToolSet() *tool.Set {
	return tool.NewSet(claude.New(), codex.New())
}
