package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/testutil"
)

// TestStatsIntegration_SingleProjectHumanOutput drives `stats <project>` end to
// end over the fixture and asserts the rendered table reports the project's
// footprint, with the prefix-sibling references excluded from its tally.
func TestStatsIntegration_SingleProjectHumanOutput(t *testing.T) {
	home := testutil.SetupFixture(t)

	stdout, err := driveStats(t, home.Dir, testutil.FixtureProjectPath())
	require.NoError(t, err)

	assert.Contains(t, stdout, "cc-port stats: "+testutil.FixtureProjectPath())
	assert.Contains(t, stdout, "file-history")
	// The sibling /Users/test/Projects/myproject-extras must never be rendered
	// as part of this project's footprint.
	assert.NotContains(t, stdout, "myproject-extras")
}

// TestStatsIntegration_AllProjectsHumanOutput drives bare `stats` end to end
// and asserts every fixture project is ranked, the richest first, with the
// witness-resolved spelling of the lossy encoded directory.
func TestStatsIntegration_AllProjectsHumanOutput(t *testing.T) {
	home := testutil.SetupFixture(t)

	stdout, err := driveStats(t, home.Dir)
	require.NoError(t, err)

	assert.Contains(t, stdout, "4 known projects (ranked by disk footprint)")
	assert.Contains(t, stdout, "/Users/test/Projects/myproject")
	// The witness recovers the space spelling; a naive decode of the encoded
	// directory name could not.
	assert.Contains(t, stdout, "/Users/test/Projects/my project")

	myprojectLine := strings.Index(stdout, "/Users/test/Projects/myproject\n")
	extrasLine := strings.Index(stdout, "/Users/test/Projects/myproject-extras")
	require.NotEqual(t, -1, myprojectLine)
	require.NotEqual(t, -1, extrasLine)
	assert.Less(t, myprojectLine, extrasLine, "the richest project is ranked above the smaller sibling")
}
