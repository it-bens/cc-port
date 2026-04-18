package transport_test

import (
	"strings"
	"testing"

	"github.com/it-bens/cc-port/internal/claude"
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
