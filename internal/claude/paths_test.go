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
