package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
)

func TestMergeResolutions_IgnoresImplicitManifestResolution(t *testing.T) {
	block := manifest.Tool{Name: "claude", Placeholders: []manifest.Placeholder{{Key: "{{PROJECT_PATH}}"}}}
	fromManifest := &manifest.Metadata{Tools: []manifest.Tool{{
		Name:         "claude",
		Placeholders: []manifest.Placeholder{{Key: "{{PROJECT_PATH}}", Resolve: "/sender/path"}},
	}}}

	resolutions, err := mergeResolutions(block, fromManifest, map[string]string{"{{PROJECT_PATH}}": "/target/path"})

	require.NoError(t, err)
	assert.Equal(t, "/target/path", resolutions["{{PROJECT_PATH}}"])
}

func TestMergeResolutions_RejectsUndeclaredManifestResolution(t *testing.T) {
	block := manifest.Tool{Name: "claude", Placeholders: []manifest.Placeholder{{Key: "{{DECLARED}}"}}}
	fromManifest := &manifest.Metadata{Tools: []manifest.Tool{{
		Name:         "claude",
		Placeholders: []manifest.Placeholder{{Key: "{{UNKNOWN}}", Resolve: "/target/path"}},
	}}}

	_, err := mergeResolutions(block, fromManifest, nil)

	var undeclared *UndeclaredResolutionKeysError
	require.ErrorAs(t, err, &undeclared)
	assert.Equal(t, "claude", undeclared.Tool)
	assert.Equal(t, []string{"{{UNKNOWN}}"}, undeclared.Keys)
}
