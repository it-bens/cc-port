package importer

import (
	"testing"

	"github.com/it-bens/cc-port/internal/manifest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunPreflight_RefusesUnresolvedDeclaredKey(t *testing.T) {
	// Arrange
	const nonImplicitKey = "{{CUSTOM_KEY}}"
	presentDeclaredKeys := map[string]struct{}{
		nonImplicitKey: {},
	}
	metadata := &manifest.Metadata{
		Placeholders: []manifest.Placeholder{
			{Key: projectPathKey, Original: "/Users/example/project"},
			{Key: homePathKey, Original: "/Users/example"},
			{Key: nonImplicitKey, Original: "/some/path"},
		},
	}
	resolutions := map[string]string{
		projectPathKey: "/Users/recipient/project",
		homePathKey:    "/Users/recipient",
		// nonImplicitKey deliberately omitted.
	}

	// Act
	err := runPreflight(presentDeclaredKeys, metadata, resolutions)

	// Assert
	require.Error(t, err)
	var missingErr *MissingResolutionsError
	require.ErrorAs(t, err, &missingErr)
	assert.Equal(t, []string{nonImplicitKey}, missingErr.Keys)
}
