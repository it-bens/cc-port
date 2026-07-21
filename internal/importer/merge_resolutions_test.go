package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
)

func TestMergeResolutions_RejectsImplicitManifestResolution(t *testing.T) {
	block := manifest.Tool{Name: "claude", Placeholders: []manifest.Placeholder{{Key: "{{PROJECT_PATH}}"}}}
	fromManifest := &manifest.Metadata{Tools: []manifest.Tool{{
		Name:         "claude",
		Placeholders: []manifest.Placeholder{{Key: "{{PROJECT_PATH}}", Resolve: "/sender/path"}},
	}}}

	_, err := MergeResolutions(block, fromManifest, map[string]string{"{{PROJECT_PATH}}": "/target/path"})

	var implicit *ImplicitKeyOverrideError
	require.ErrorAs(t, err, &implicit)
	assert.Equal(t, "claude", implicit.Tool)
	assert.Equal(t, []string{"{{PROJECT_PATH}}"}, implicit.Keys)
	assert.Equal(t, "--from-manifest", implicit.Surface)
}

func TestMergeResolutions_MergesDeclaredManifestResolution(t *testing.T) {
	block := manifest.Tool{Name: "claude", Placeholders: []manifest.Placeholder{{Key: "{{DECLARED}}"}}}
	fromManifest := &manifest.Metadata{Tools: []manifest.Tool{{
		Name:         "claude",
		Placeholders: []manifest.Placeholder{{Key: "{{DECLARED}}", Resolve: "/target/path"}},
	}}}

	resolutions, err := MergeResolutions(block, fromManifest, nil)

	require.NoError(t, err)
	assert.Equal(t, "/target/path", resolutions["{{DECLARED}}"])
}

func TestMergeResolutions_RejectsRelativeResolutionValue(t *testing.T) {
	block := manifest.Tool{Name: "claude", Placeholders: []manifest.Placeholder{{Key: "{{DECLARED}}", Resolve: "relative/path"}}}

	_, err := MergeResolutions(block, nil, nil)

	var invalid *archive.InvalidResolutionsError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, []string{"{{DECLARED}}"}, invalid.Keys)
}

func TestMergeResolutions_RejectsUndeclaredManifestResolution(t *testing.T) {
	block := manifest.Tool{Name: "claude", Placeholders: []manifest.Placeholder{{Key: "{{DECLARED}}"}}}
	fromManifest := &manifest.Metadata{Tools: []manifest.Tool{{
		Name:         "claude",
		Placeholders: []manifest.Placeholder{{Key: "{{UNKNOWN}}", Resolve: "/target/path"}},
	}}}

	_, err := MergeResolutions(block, fromManifest, nil)

	var undeclared *UndeclaredResolutionKeysError
	require.ErrorAs(t, err, &undeclared)
	assert.Equal(t, "claude", undeclared.Tool)
	assert.Equal(t, []string{"{{UNKNOWN}}"}, undeclared.Keys)
}
