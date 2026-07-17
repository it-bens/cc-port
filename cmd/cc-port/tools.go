package main

import (
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// newToolSet is the composition root: the one place cc-port lists its
// supported tools. Command packages import internal/tool only; every
// adapter import lives here.
func newToolSet() *tool.Set {
	return tool.NewSet(claude.New())
}
