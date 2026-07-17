package claude_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/it-bens/cc-port/internal/tool/claude"
)

// TestSessionKeyedGroups_CategoriesAreDeclaredCategories asserts every
// group's Category matches an entry in claude.New().Categories(). Adding a
// session-keyed group with a Category outside the declared registry must
// fail this test.
func TestSessionKeyedGroups_CategoriesAreDeclaredCategories(t *testing.T) {
	known := make(map[string]bool)
	for _, category := range claude.New().Categories() {
		known[category.Name] = true
	}
	for group := range claude.SessionKeyedGroups() {
		assert.True(t, known[group.Category],
			"SessionKeyedGroup %q has Category %q not in the declared category registry",
			group.Name, group.Category)
	}
}
