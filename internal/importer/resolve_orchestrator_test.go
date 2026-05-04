package importer_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
)

func TestResolvePlaceholders_FiltersImplicitKey(t *testing.T) {
	var promptedKeys []string
	prompter := func(unresolved []string) (map[string]string, error) {
		promptedKeys = append(promptedKeys, unresolved...)
		out := make(map[string]string, len(unresolved))
		for _, k := range unresolved {
			out[k] = "P-" + k
		}
		return out, nil
	}
	unresolved := []string{"{{PROJECT_PATH}}", "{{HOME}}", "{{ORG}}"}

	resolutions, err := importer.ResolvePlaceholders(unresolved, nil, prompter)
	require.NoError(t, err)
	for key := range resolutions {
		assert.False(t, importer.IsImplicitKey(key),
			"implicit key %q leaked into resolutions", key)
	}
	assert.Equal(t, "P-{{ORG}}", resolutions["{{ORG}}"])
	for _, key := range promptedKeys {
		assert.False(t, importer.IsImplicitKey(key),
			"implicit key %q reached the prompter", key)
	}
}

func TestResolvePlaceholders_ManifestKnownMergedFirst(t *testing.T) {
	var promptedKeys []string
	prompter := func(unresolved []string) (map[string]string, error) {
		promptedKeys = append(promptedKeys, unresolved...)
		out := map[string]string{}
		for _, k := range unresolved {
			out[k] = "FROM_PROMPT"
		}
		return out, nil
	}
	fromManifest := &manifest.Metadata{
		Placeholders: []manifest.Placeholder{
			{Key: "{{HOST}}", Resolve: "/srv/host"},
			{Key: "{{ORG}}", Resolve: ""}, // empty ignored
		},
	}
	unresolved := []string{"{{HOST}}", "{{ORG}}"}

	resolutions, err := importer.ResolvePlaceholders(unresolved, fromManifest, prompter)
	require.NoError(t, err)
	assert.Equal(t, "/srv/host", resolutions["{{HOST}}"])
	assert.Equal(t, "FROM_PROMPT", resolutions["{{ORG}}"])
	assert.Equal(t, []string{"{{ORG}}"}, promptedKeys)
}

func TestResolvePlaceholders_ManifestImplicitKeyDropped(t *testing.T) {
	fromManifest := &manifest.Metadata{
		Placeholders: []manifest.Placeholder{
			{Key: "{{PROJECT_PATH}}", Resolve: "/sender/path"},
		},
	}
	prompter := func(_ []string) (map[string]string, error) { return nil, nil }
	resolutions, err := importer.ResolvePlaceholders(
		[]string{"{{HOME}}"}, fromManifest, prompter)
	require.NoError(t, err)
	for key := range resolutions {
		assert.False(t, importer.IsImplicitKey(key))
	}
}

func TestResolvePlaceholders_PrompterErrorPropagates(t *testing.T) {
	sentinelPrompterErr := errors.New("user canceled")
	prompter := func(_ []string) (map[string]string, error) {
		return nil, sentinelPrompterErr
	}
	_, err := importer.ResolvePlaceholders([]string{"{{ORG}}"}, nil, prompter)
	require.ErrorIs(t, err, sentinelPrompterErr)
}

func TestResolvePlaceholders_NilPrompterRejectedWhenNeeded(t *testing.T) {
	_, err := importer.ResolvePlaceholders([]string{"{{ORG}}"}, nil, nil)
	require.Error(t, err)
}

func TestResolvePlaceholders_NilPrompterAcceptedWhenNotNeeded(t *testing.T) {
	fromManifest := &manifest.Metadata{
		Placeholders: []manifest.Placeholder{{Key: "{{ORG}}", Resolve: "/x"}},
	}
	resolutions, err := importer.ResolvePlaceholders(
		[]string{"{{ORG}}"}, fromManifest, nil)
	require.NoError(t, err)
	assert.Equal(t, "/x", resolutions["{{ORG}}"])
}
