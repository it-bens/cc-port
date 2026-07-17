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

	got := make([]string, 0, len(want))
	for target := range UserWideRewriteTargets() {
		got = append(got, target.Name)
	}
	require.Len(t, got, len(want))

	for i, name := range got {
		assert.Equal(t, want[i], name, "position %d", i)
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

	got := make(map[string]string)
	for target := range UserWideRewriteTargets() {
		got[target.Name] = target.RewritePath(home)
	}

	assert.Equal(t, want, got)
}
