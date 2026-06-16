package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/testutil"
)

func TestDiscoverAndPromptPlaceholders_AlwaysDeclaresProjectDir(t *testing.T) {
	home := testutil.SetupFixture(t)
	projectPath := testutil.FixtureProjectPath()

	placeholders, err := discoverAndPromptPlaceholders(home, projectPath)
	require.NoError(t, err)

	var original string
	found := false
	for _, placeholder := range placeholders {
		if placeholder.Key == "{{PROJECT_DIR}}" {
			found = true
			original = placeholder.Original
		}
	}
	require.True(t, found, "{{PROJECT_DIR}} must always be declared")
	assert.Equal(t, home.ProjectDir(projectPath), original)
}
