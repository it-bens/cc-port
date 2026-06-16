package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWithImplicitAnchors_CallerWinsOverImplicitProjectDir pins the no-clobber
// invariant of the unexported helper: a caller-supplied {{PROJECT_DIR}} is left
// untouched. The cmd layer refuses user-supplied resolutions for implicit keys,
// so this precedence is not reachable through importer.Run and is tested here.
func TestWithImplicitAnchors_CallerWinsOverImplicitProjectDir(t *testing.T) {
	got := withImplicitAnchors(
		map[string]string{projectDirKey: "/explicit/dir"},
		"/p", "/h", "/derived/dir",
	)
	assert.Equal(t, "/explicit/dir", got[projectDirKey])
}
