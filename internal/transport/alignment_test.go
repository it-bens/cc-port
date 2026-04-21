package transport_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/transport"
)

func TestSessionKeyedTargets_AlignedWithGroups(t *testing.T) {
	if len(transport.SessionKeyedTargets) != len(claude.SessionKeyedGroups) {
		t.Fatalf("length mismatch: transport.SessionKeyedTargets=%d claude.SessionKeyedGroups=%d",
			len(transport.SessionKeyedTargets), len(claude.SessionKeyedGroups))
	}
	for index, target := range transport.SessionKeyedTargets {
		if target.Group != claude.SessionKeyedGroups[index].Name {
			t.Errorf("index %d: Group=%q, want %q",
				index, target.Group, claude.SessionKeyedGroups[index].Name)
		}
	}
}

func TestSessionKeyedTargets_ZipPrefixesUnique(t *testing.T) {
	seen := make(map[string]int, len(transport.SessionKeyedTargets))
	for index, target := range transport.SessionKeyedTargets {
		if previous, exists := seen[target.ZipPrefix]; exists {
			t.Errorf("duplicate ZipPrefix %q at indices %d and %d",
				target.ZipPrefix, previous, index)
		}
		seen[target.ZipPrefix] = index
	}
}

func TestSessionKeyedTargets_ZipPrefixesTerminatedWithSlash(t *testing.T) {
	for index, target := range transport.SessionKeyedTargets {
		if !strings.HasSuffix(target.ZipPrefix, "/") {
			t.Errorf("index %d (%s): ZipPrefix %q must end with '/'",
				index, target.Group, target.ZipPrefix)
		}
	}
}

// TestSessionKeyedTargets_FilesRootedUnderHomeBaseDir checks that paths a
// collector places into ProjectLocations for each session-keyed group are
// rooted under the matching SessionKeyedTargets[i].HomeBaseDir(home). A
// drifted collector that populated the wrong ProjectLocations field, or a
// transport target whose HomeBaseDir no longer matched the group's on-disk
// location, would silently produce wrong archive prefixes and wrong import
// destinations. The name-and-length alignment test above cannot catch this.
func TestSessionKeyedTargets_FilesRootedUnderHomeBaseDir(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	locations, err := claude.LocateProject(claudeHome, "/Users/test/Projects/myproject")
	require.NoError(t, err)

	for index, target := range transport.SessionKeyedTargets {
		group := claude.SessionKeyedGroups[index]
		base := target.HomeBaseDir(claudeHome)
		for _, path := range group.Files(locations) {
			assert.Truef(t, strings.HasPrefix(path, base+"/") || path == base,
				"group %q path %q must live under HomeBaseDir %q",
				group.Name, path, base)
		}
	}
}
