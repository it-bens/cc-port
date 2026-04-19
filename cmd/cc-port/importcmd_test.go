package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseResolutionFlags_Empty(t *testing.T) {
	parsed, err := parseResolutionFlags(nil)
	require.NoError(t, err)
	assert.Empty(t, parsed)
}

func TestParseResolutionFlags_SingleEntry(t *testing.T) {
	parsed, err := parseResolutionFlags([]string{"{{HOME}}=/Users/me"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"{{HOME}}": "/Users/me"}, parsed)
}

func TestParseResolutionFlags_MultipleEntries(t *testing.T) {
	parsed, err := parseResolutionFlags([]string{
		"{{HOME}}=/Users/me",
		"{{WORK}}=/opt/work",
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"{{HOME}}": "/Users/me",
		"{{WORK}}": "/opt/work",
	}, parsed)
}

func TestParseResolutionFlags_ValueWithEquals(t *testing.T) {
	// Only the first '=' splits key from value; subsequent ones are preserved.
	parsed, err := parseResolutionFlags([]string{"{{URL}}=/opt/app?x=1"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"{{URL}}": "/opt/app?x=1"}, parsed)
}

func TestParseResolutionFlags_RejectsMissingEquals(t *testing.T) {
	_, err := parseResolutionFlags([]string{"{{HOME}}"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "KEY=VALUE")
}

func TestParseResolutionFlags_RejectsEmptyKey(t *testing.T) {
	_, err := parseResolutionFlags([]string{"=/Users/me"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty key")
}

func TestParseResolutionFlags_RejectsProjectPath(t *testing.T) {
	_, err := parseResolutionFlags([]string{"{{PROJECT_PATH}}=/Users/me/project"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "{{PROJECT_PATH}}")
}

func TestParseResolutionFlags_RejectsDuplicateKey(t *testing.T) {
	_, err := parseResolutionFlags([]string{
		"{{HOME}}=/Users/alice",
		"{{HOME}}=/Users/bob",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "{{HOME}}")
}
