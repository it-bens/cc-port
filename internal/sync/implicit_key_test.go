package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/it-bens/cc-port/internal/manifest"
)

func TestComputeUnresolved_TreatsProjectDirAsImplicit(t *testing.T) {
	declared := []manifest.Placeholder{{Key: "{{PROJECT_DIR}}"}}
	got := computeUnresolved(declared, nil, "/target")
	assert.Empty(t, got, "{{PROJECT_DIR}} is implicit; pull must not flag it unresolved")
}
