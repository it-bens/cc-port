package claude_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

func TestSessionKeyedTargets_ZipPrefixesUnique(t *testing.T) {
	seen := make(map[string]int)
	index := 0
	for target := range claude.SessionKeyedGroups() {
		if previous, exists := seen[target.ZipPrefix]; exists {
			t.Errorf("duplicate ZipPrefix %q at indices %d and %d",
				target.ZipPrefix, previous, index)
		}
		seen[target.ZipPrefix] = index
		index++
	}
}

func TestSessionKeyedTargets_ZipPrefixesTerminatedWithSlash(t *testing.T) {
	index := 0
	for target := range claude.SessionKeyedGroups() {
		if !strings.HasSuffix(target.ZipPrefix, "/") {
			t.Errorf("index %d (%s): ZipPrefix %q must end with '/'",
				index, target.Name, target.ZipPrefix)
		}
		index++
	}
}

func TestSessionKeyedTargets_FilesRootedUnderHomeBaseDir(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	locations, err := claude.LocateProject(claudeHome, "/Users/test/Projects/myproject")
	require.NoError(t, err)

	for target := range claude.SessionKeyedGroups() {
		base := target.HomeBaseDir(claudeHome)
		for _, path := range target.Files(locations) {
			assert.Truef(t, strings.HasPrefix(path, base+"/") || path == base,
				"group %q path %q must live under HomeBaseDir %q",
				target.Name, path, base)
		}
	}
}
