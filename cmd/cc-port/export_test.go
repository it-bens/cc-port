package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveHomeAnchor_StripsTrailingSlash(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome+"/")

	resolved, err := resolveHomeAnchor()

	require.NoError(t, err)
	assert.False(t, strings.HasSuffix(resolved, "/"), "anchor must not have trailing slash, got %q", resolved)
}

func TestResolveHomeAnchor_RejectsRoot(t *testing.T) {
	t.Setenv("HOME", "/")

	_, err := resolveHomeAnchor()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid home directory")
}

func TestResolveHomeAnchor_RejectsEmpty(t *testing.T) {
	t.Setenv("HOME", "")

	_, err := resolveHomeAnchor()

	require.Error(t, err)
}

func TestResolveHomeAnchor_RejectsNonAbsolute(t *testing.T) {
	t.Setenv("HOME", "relative/home/path")

	_, err := resolveHomeAnchor()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid home directory")
}

func TestResolveHomeAnchor_FollowsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test exercises Linux/macOS behaviour")
	}
	tempDir := t.TempDir()
	realHome := filepath.Join(tempDir, "real-home")
	require.NoError(t, os.MkdirAll(realHome, 0o755))
	linkHome := filepath.Join(tempDir, "link-home")
	require.NoError(t, os.Symlink(realHome, linkHome))
	t.Setenv("HOME", linkHome)

	resolved, err := resolveHomeAnchor()

	require.NoError(t, err)
	expected, err := filepath.EvalSymlinks(realHome)
	require.NoError(t, err)
	assert.Equal(t, expected, resolved, "anchor must resolve through symlink to mirror projectPath treatment")
}
