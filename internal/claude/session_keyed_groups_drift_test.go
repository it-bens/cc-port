package claude_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/manifest"
)

// TestSessionKeyedGroups_CategoriesAreManifestCategories asserts every
// group's Category matches an entry in manifest.AllCategories. Adding a
// session-keyed group with a Category outside the manifest registry must
// fail this test.
func TestSessionKeyedGroups_CategoriesAreManifestCategories(t *testing.T) {
	known := make(map[string]bool, len(manifest.AllCategories))
	for _, spec := range manifest.AllCategories {
		known[spec.Name] = true
	}
	for _, group := range claude.SessionKeyedGroups {
		assert.True(t, known[group.Category],
			"SessionKeyedGroup %q has Category %q not in manifest.AllCategories",
			group.Name, group.Category)
	}
}
