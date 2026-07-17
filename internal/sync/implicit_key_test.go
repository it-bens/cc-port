package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/it-bens/cc-port/internal/manifest"
)

func TestComputeUnresolved_TreatsAnchorKeyAsImplicit(t *testing.T) {
	block := manifest.Tool{Name: "claude", Placeholders: []manifest.Placeholder{{Key: "{{PROJECT_DIR}}"}}}
	anchors := map[string]string{"{{PROJECT_DIR}}": "/target/encoded"}
	got := computeUnresolved(block, nil, anchors)
	assert.Empty(t, got, "a key present in the tool's implicit anchors must not be flagged unresolved")
}

func TestComputeUnresolved_FlagsKeyWithNoResolution(t *testing.T) {
	block := manifest.Tool{Name: "claude", Placeholders: []manifest.Placeholder{{Key: "{{ORG}}"}}}
	got := computeUnresolved(block, nil, map[string]string{})
	assert.Equal(t, []string{"{{ORG}}"}, got)
}
