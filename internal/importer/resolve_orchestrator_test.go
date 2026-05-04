package importer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
)

func TestResolvePlaceholders_MergesManifestResolveValues(t *testing.T) {
	unresolved := []string{"{{HOST}}"}
	fromManifest := &manifest.Metadata{
		Placeholders: []manifest.Placeholder{
			{Key: "{{HOST}}", Resolve: "/srv/host"},
		},
	}

	resolutions, err := importer.ResolvePlaceholders(unresolved, fromManifest)

	require.NoError(t, err)
	assert.Equal(t, "/srv/host", resolutions["{{HOST}}"])
}

func TestResolvePlaceholders_IgnoresEmptyManifestResolveValues(t *testing.T) {
	unresolved := []string{"{{ORG}}"}
	fromManifest := &manifest.Metadata{
		Placeholders: []manifest.Placeholder{
			{Key: "{{ORG}}", Resolve: ""},
		},
	}

	_, err := importer.ResolvePlaceholders(unresolved, fromManifest)

	var missing *importer.MissingResolutionsError
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, []string{"{{ORG}}"}, missing.Keys)
}

func TestResolvePlaceholders_FiltersImplicitFromManifestResolve(t *testing.T) {
	unresolved := []string{"{{HOME}}"}
	fromManifest := &manifest.Metadata{
		Placeholders: []manifest.Placeholder{
			{Key: "{{HOME}}", Resolve: "/Users/wrong"},
		},
	}

	resolutions, err := importer.ResolvePlaceholders(unresolved, fromManifest)

	require.NoError(t, err)
	_, has := resolutions["{{HOME}}"]
	assert.False(t, has,
		"orchestrator must filter {{HOME}} as implicit; importer.Run is responsible for the value")
}

func TestResolvePlaceholders_ErrorsOnUnresolvedNonImplicit(t *testing.T) {
	unresolved := []string{"{{CUSTOM_KEY}}"}

	_, err := importer.ResolvePlaceholders(unresolved, nil)

	require.Error(t, err)
	var missing *importer.MissingResolutionsError
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, []string{"{{CUSTOM_KEY}}"}, missing.Keys)
}
