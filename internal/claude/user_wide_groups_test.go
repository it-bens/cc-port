package claude

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserWideRewriteTargetsHasExpectedNamesInOrder(t *testing.T) {
	want := []string{
		"settings",
		"plugins/installed_plugins",
		"plugins/known_marketplaces",
	}

	require.Len(t, UserWideRewriteTargets, len(want))

	for i, target := range UserWideRewriteTargets {
		assert.Equal(t, want[i], target.Name, "position %d", i)
	}
}

func TestUserWideRewriteTargetsPathResolvesAgainstHome(t *testing.T) {
	home := &Home{
		Dir:        "/home/user/.claude",
		ConfigFile: "/home/user/.claude.json",
	}
	want := map[string]string{
		"settings":                   "/home/user/.claude/settings.json",
		"plugins/installed_plugins":  "/home/user/.claude/plugins/installed_plugins.json",
		"plugins/known_marketplaces": "/home/user/.claude/plugins/known_marketplaces.json",
	}

	got := make(map[string]string, len(UserWideRewriteTargets))
	for _, target := range UserWideRewriteTargets {
		got[target.Name] = target.Path(home)
	}

	assert.Equal(t, want, got)
}
