package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/testutil"
)

// TestExportManifest_AlwaysDeclaresProjectDir asserts the written export
// manifest declares {{PROJECT_DIR}} even when project content surfaces no
// discoverable anchor. The encoded dir lives in session-subdir bodies that
// discovery does not scan, so the adapter declares it unconditionally.
func TestExportManifest_AlwaysDeclaresProjectDir(t *testing.T) {
	home, _, manifestPath := driveExportManifest(t, testutil.FixtureProjectPath())

	metadata, err := manifest.ReadManifest(manifestPath)
	require.NoError(t, err)

	block, ok := metadata.ToolBlock("claude")
	require.True(t, ok)

	var projectDir *manifest.Placeholder
	for i := range block.Placeholders {
		if block.Placeholders[i].Key == "{{PROJECT_DIR}}" {
			projectDir = &block.Placeholders[i]
		}
	}
	require.NotNil(t, projectDir, "{{PROJECT_DIR}} must always be declared")
	assert.Equal(t, home.ProjectDir(testutil.FixtureProjectPath()), projectDir.Original)
}
