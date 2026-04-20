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

func TestNewHome_OverrideNormalisesRelative(t *testing.T) {
	workingDir, err := os.Getwd()
	require.NoError(t, err)

	home, err := claude.NewHome("relative/subdir")
	require.NoError(t, err)

	assert.True(t, filepath.IsAbs(home.Dir), "Dir must be absolute, got %q", home.Dir)
	assert.Equal(t, filepath.Join(workingDir, "relative/subdir"), home.Dir)
	assert.Equal(t, filepath.Join(workingDir, "relative/subdir")+".json", home.ConfigFile)
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
	require.NoError(t, os.MkdirAll(realProjectDir, 0o750))

	linkDir := filepath.Join(realTempDir, "link")
	require.NoError(t, os.Symlink(filepath.Join(realTempDir, "real"), linkDir))

	t.Run("resolves symlink in existing path", func(t *testing.T) {
		resolved, err := claude.ResolveProjectPath(filepath.Join(linkDir, "project"))
		require.NoError(t, err)
		assert.Equal(t, realProjectDir, resolved)
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
}

func TestHome_DerivesPaths(t *testing.T) {
	home := claude.Home{
		Dir:        "/home/user/.claude",
		ConfigFile: "/home/user/.claude.json",
	}

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"ProjectsDir", home.ProjectsDir(), "/home/user/.claude/projects"},
		{"HistoryFile", home.HistoryFile(), "/home/user/.claude/history.jsonl"},
		{"SessionsDir", home.SessionsDir(), "/home/user/.claude/sessions"},
		{"SettingsFile", home.SettingsFile(), "/home/user/.claude/settings.json"},
		{"RulesDir", home.RulesDir(), "/home/user/.claude/rules"},
		{"FileHistoryDir", home.FileHistoryDir(), "/home/user/.claude/file-history"},
		{"TodosDir", home.TodosDir(), "/home/user/.claude/todos"},
		{"UsageDataDir", home.UsageDataDir(), "/home/user/.claude/usage-data"},
		{"PluginsDataDir", home.PluginsDataDir(), "/home/user/.claude/plugins/data"},
		{"TasksDir", home.TasksDir(), "/home/user/.claude/tasks"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.got)
		})
	}
}
