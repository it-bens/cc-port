package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsImplicitKey_IncludesProjectDir(t *testing.T) {
	assert.True(t, IsImplicitKey(projectDirKey))
	assert.True(t, IsImplicitKey(projectPathKey))
	assert.True(t, IsImplicitKey(homePathKey))
	assert.False(t, IsImplicitKey("{{CUSTOM}}"))
}

func TestWithImplicitAnchors_InjectsProjectDir(t *testing.T) {
	got := withImplicitAnchors(
		map[string]string{},
		"/new/project", "/home/user",
		"/home/user/.claude/projects/-new-project",
	)
	assert.Equal(t, "/home/user/.claude/projects/-new-project", got[projectDirKey])
	assert.Equal(t, "/new/project", got[projectPathKey])
	assert.Equal(t, "/home/user", got[homePathKey])
}

func TestWithImplicitAnchors_CallerWinsOverImplicitProjectDir(t *testing.T) {
	got := withImplicitAnchors(
		map[string]string{projectDirKey: "/explicit/dir"},
		"/p", "/h", "/derived/dir",
	)
	assert.Equal(t, "/explicit/dir", got[projectDirKey])
}
