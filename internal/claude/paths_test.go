package claude_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
)

func TestEncodePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple path",
			input:    "/Users/test/Projects/myproject",
			expected: "-Users-test-Projects-myproject",
		},
		{
			name:     "path with dots",
			input:    "/tmp/cc-port.test/foo",
			expected: "-tmp-cc-port-test-foo",
		},
		{
			name:     "path with spaces",
			input:    "/tmp/My Project/bar",
			expected: "-tmp-My-Project-bar",
		},
		{
			name:     "path with hyphens",
			input:    "/Users/test/my-project",
			expected: "-Users-test-my-project",
		},
		{
			name:     "path with mixed special chars",
			input:    "/tmp/cc-port.test/My Project-v2",
			expected: "-tmp-cc-port-test-My-Project-v2",
		},
		{
			name:     "root path",
			input:    "/",
			expected: "-",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := claude.EncodePath(testCase.input)
			assert.Equal(t, testCase.expected, got)
		})
	}
}

func TestNewHome_Default(t *testing.T) {
	home, err := claude.NewHome("")
	require.NoError(t, err)

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(homeDir, ".claude"), home.Dir)
	assert.Equal(t, filepath.Join(homeDir, ".claude.json"), home.ConfigFile)
}

func TestNewHome_Override(t *testing.T) {
	override := "/tmp/test-claude"
	home, err := claude.NewHome(override)
	require.NoError(t, err)

	assert.Equal(t, "/tmp/test-claude", home.Dir)
	assert.Equal(t, "/tmp/test-claude.json", home.ConfigFile)
}

func TestHome_ProjectDir(t *testing.T) {
	home := claude.Home{
		Dir:        "/home/user/.claude",
		ConfigFile: "/home/user/.claude.json",
	}

	got := home.ProjectDir("/Users/test/Projects/myproject")
	assert.Equal(t, "/home/user/.claude/projects/-Users-test-Projects-myproject", got)
}

func TestResolveProjectPath(t *testing.T) {
	tempDir := t.TempDir()

	// Resolve tempDir itself so /var -> /private/var symlinks on macOS do not
	// make the assertions below compare against the pre-symlink form.
	realTempDir, err := filepath.EvalSymlinks(tempDir)
	require.NoError(t, err)

	realProjectDir := filepath.Join(realTempDir, "real", "project")
	require.NoError(t, os.MkdirAll(realProjectDir, 0o755))

	linkDir := filepath.Join(realTempDir, "link")
	require.NoError(t, os.Symlink(filepath.Join(realTempDir, "real"), linkDir))

	t.Run("resolves symlink in existing path", func(t *testing.T) {
		resolved, err := claude.ResolveProjectPath(filepath.Join(linkDir, "project"))
		require.NoError(t, err)
		assert.Equal(t, realProjectDir, resolved)
	})

	t.Run("resolves symlink in parent when leaf does not exist", func(t *testing.T) {
		resolved, err := claude.ResolveProjectPath(filepath.Join(linkDir, "new-project"))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(realTempDir, "real", "new-project"), resolved)
	})

	t.Run("preserves multiple missing trailing components", func(t *testing.T) {
		resolved, err := claude.ResolveProjectPath(filepath.Join(linkDir, "a", "b", "c"))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(realTempDir, "real", "a", "b", "c"), resolved)
	})

	t.Run("returns absolute form of relative path", func(t *testing.T) {
		workingDir, err := os.Getwd()
		require.NoError(t, err)
		realWorkingDir, err := filepath.EvalSymlinks(workingDir)
		require.NoError(t, err)

		resolved, err := claude.ResolveProjectPath("nonexistent-rel-path")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(realWorkingDir, "nonexistent-rel-path"), resolved)
	})

	t.Run("fully existing path without symlinks is unchanged", func(t *testing.T) {
		resolved, err := claude.ResolveProjectPath(realProjectDir)
		require.NoError(t, err)
		assert.Equal(t, realProjectDir, resolved)
	})
}

func TestHome_Paths(t *testing.T) {
	home := claude.Home{
		Dir:        "/home/user/.claude",
		ConfigFile: "/home/user/.claude.json",
	}

	assert.Equal(t, "/home/user/.claude/projects", home.ProjectsDir())
	assert.Equal(t, "/home/user/.claude/history.jsonl", home.HistoryFile())
	assert.Equal(t, "/home/user/.claude/sessions", home.SessionsDir())
	assert.Equal(t, "/home/user/.claude/settings.json", home.SettingsFile())
	assert.Equal(t, "/home/user/.claude/rules", home.RulesDir())
	assert.Equal(t, "/home/user/.claude/file-history", home.FileHistoryDir())
}
