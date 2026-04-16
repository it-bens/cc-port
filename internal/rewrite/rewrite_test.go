package rewrite_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/rewrite"
)

// splitLines splits data on newlines and returns a slice of strings.
func splitLines(data []byte) []string {
	parts := bytes.Split(data, []byte("\n"))
	result := make([]string, len(parts))
	for index, part := range parts {
		result[index] = string(part)
	}
	return result
}

func TestReplaceInBytes(t *testing.T) {
	t.Run("match replaces all occurrences and returns count", func(t *testing.T) {
		input := []byte(`{"project": "/old/project", "other": "/old/project"}`)
		result, count := rewrite.ReplaceInBytes(input, "/old/project", "/new/project")
		assert.Equal(t, 2, count)
		assert.Contains(t, string(result), "/new/project")
		assert.NotContains(t, string(result), "/old/project")
	})

	t.Run("no match returns original data and zero count", func(t *testing.T) {
		input := []byte(`{"project": "/other/project"}`)
		result, count := rewrite.ReplaceInBytes(input, "/old/project", "/new/project")
		assert.Equal(t, 0, count)
		assert.Equal(t, input, result)
	})
}

func TestSessionsIndex(t *testing.T) {
	t.Run("rewrites matching entries and preserves unknown fields", func(t *testing.T) {
		input := []byte(`{
			"version": 1,
			"entries": [
				{
					"sessionId": "abc123",
					"projectPath": "/old/project",
					"fullPath": "/home/user/.claude/projects/-old-project",
					"customField": "preserved"
				},
				{
					"sessionId": "def456",
					"projectPath": "/other/project",
					"fullPath": "/home/user/.claude/projects/-other-project",
					"customField": "also-preserved"
				}
			]
		}`)

		result, count, err := rewrite.SessionsIndex(
			input,
			"/old/project",
			"/new/project",
			"/home/user/.claude/projects/-old-project",
			"/home/user/.claude/projects/-new-project",
		)

		require.NoError(t, err)
		assert.Equal(t, 1, count)

		var decoded map[string]interface{}
		require.NoError(t, json.Unmarshal(result, &decoded))

		entries := decoded["entries"].([]interface{})
		assert.Len(t, entries, 2)

		firstEntry := entries[0].(map[string]interface{})
		assert.Equal(t, "/new/project", firstEntry["projectPath"])
		assert.Equal(t, "/home/user/.claude/projects/-new-project", firstEntry["fullPath"])
		assert.Equal(t, "preserved", firstEntry["customField"])

		secondEntry := entries[1].(map[string]interface{})
		assert.Equal(t, "/other/project", secondEntry["projectPath"])
		assert.Equal(t, "/home/user/.claude/projects/-other-project", secondEntry["fullPath"])
		assert.Equal(t, "also-preserved", secondEntry["customField"])
	})

	t.Run("returns zero count when no entries match", func(t *testing.T) {
		input := []byte(
			`{"version": 1, "entries": [{"sessionId": "abc", "projectPath": "/other/project", "fullPath": "/some/path"}]}`,
		)
		_, count, err := rewrite.SessionsIndex(input, "/old/project", "/new/project", "/old/dir", "/new/dir")
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("returns error on invalid JSON", func(t *testing.T) {
		_, _, err := rewrite.SessionsIndex([]byte(`not json`), "/old", "/new", "/old/dir", "/new/dir")
		assert.Error(t, err)
	})
}

func TestHistoryJSONL(t *testing.T) {
	t.Run("rewrites matching lines and preserves non-matching lines", assertHistoryJSONLRewritesMatching)

	t.Run("returns zero count when no lines match", func(t *testing.T) {
		input := []byte(`{"project":"/other/project","command":"ls"}` + "\n")
		_, count, err := rewrite.HistoryJSONL(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("preserves malformed JSON lines verbatim and continues", func(t *testing.T) {
		good := `{"project":"/old/project","display":"a"}`
		bad := `{ this is not valid json`
		alsoGood := `{"project":"/old/project","display":"b"}`
		input := []byte(good + "\n" + bad + "\n" + alsoGood + "\n")

		result, count, err := rewrite.HistoryJSONL(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.Equal(t, 2, count, "two well-formed lines should be rewritten")

		lines := splitLines(result)
		assert.Equal(t, bad, lines[1], "malformed line must be preserved verbatim")
		assert.Contains(t, lines[0], "/new/project")
		assert.Contains(t, lines[2], "/new/project")
	})

	t.Run("rewrites path occurrences inside non-project fields", func(t *testing.T) {
		input := []byte(`{"project":"/old/project","display":"open /old/project/main.go please"}` + "\n")
		result, count, err := rewrite.HistoryJSONL(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.Equal(t, 1, count)
		assert.NotContains(t, string(result), "/old/project")
		assert.Contains(t, string(result), "/new/project/main.go")
	})

	t.Run("does not rewrite a path that is a prefix of another path", func(t *testing.T) {
		input := []byte(`{"project":"/old/project-extras","display":"unrelated"}` + "\n")
		result, count, err := rewrite.HistoryJSONL(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.Equal(t, 0, count, "path-boundary protection must skip prefix collision")
		assert.Contains(t, string(result), "/old/project-extras")
		assert.NotContains(t, string(result), "/new/project-extras")
	})
}

func assertHistoryJSONLRewritesMatching(t *testing.T) {
	line1 := `{"project":"/old/project","command":"ls"}`
	line2 := `{"project":"/other/project","command":"pwd"}`
	line3 := `{"project":"/old/project","command":"git status"}`
	input := []byte(line1 + "\n" + line2 + "\n" + line3 + "\n")

	result, count, err := rewrite.HistoryJSONL(input, "/old/project", "/new/project")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	lines := splitLines(result)
	// Last element should be empty string (trailing newline)
	assert.Empty(t, lines[len(lines)-1])

	var entry1, entry2, entry3 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry1))
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &entry2))
	require.NoError(t, json.Unmarshal([]byte(lines[2]), &entry3))

	assert.Equal(t, "/new/project", entry1["project"])
	assert.Equal(t, "ls", entry1["command"])
	assert.Equal(t, "/other/project", entry2["project"])
	assert.Equal(t, "/new/project", entry3["project"])
	assert.Equal(t, "git status", entry3["command"])
}

func TestReplacePathInBytes(t *testing.T) {
	t.Run("replaces full-component matches", func(t *testing.T) {
		input := []byte(`prefix /a/foo/bar /a/foo "/a/foo" /a/foo`)
		result, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 4, count)
		assert.NotContains(t, string(result), "/a/foo")
	})

	t.Run("does not corrupt prefix collisions on the right boundary", func(t *testing.T) {
		input := []byte(`/a/foo-extras /a/foo /a/foo2 /a/foo_bar /a/foo.txt`)
		result, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 1, count, "only the standalone /a/foo should match")
		assert.Contains(t, string(result), "/a/foo-extras")
		assert.Contains(t, string(result), "/a/foo2")
		assert.Contains(t, string(result), "/a/foo_bar")
		assert.Contains(t, string(result), "/a/foo.txt")
		assert.Contains(t, string(result), "/x/qux ")
	})

	t.Run("matches at end of buffer", func(t *testing.T) {
		input := []byte(`tail /a/foo`)
		result, count := rewrite.ReplacePathInBytes(input, "/a/foo", "/x/qux")
		assert.Equal(t, 1, count)
		assert.Equal(t, `tail /x/qux`, string(result))
	})

	t.Run("returns original on empty inputs", func(t *testing.T) {
		input := []byte(`some data`)
		result, count := rewrite.ReplacePathInBytes(input, "", "/x")
		assert.Equal(t, 0, count)
		assert.Equal(t, input, result)
	})
}

func TestSessionFile(t *testing.T) {
	t.Run("rewrites cwd when it starts with oldProject", func(t *testing.T) {
		input := []byte(`{"cwd":"/old/project/subdir","extraField":"value"}`)
		result, changed, err := rewrite.SessionFile(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.True(t, changed)

		var decoded map[string]interface{}
		require.NoError(t, json.Unmarshal(result, &decoded))
		assert.Equal(t, "/new/project/subdir", decoded["cwd"])
		assert.Equal(t, "value", decoded["extraField"])
	})

	t.Run("rewrites cwd when it equals oldProject exactly", func(t *testing.T) {
		input := []byte(`{"cwd":"/old/project"}`)
		result, changed, err := rewrite.SessionFile(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.True(t, changed)

		var decoded map[string]interface{}
		require.NoError(t, json.Unmarshal(result, &decoded))
		assert.Equal(t, "/new/project", decoded["cwd"])
	})

	t.Run("does not rewrite cwd when it does not match oldProject", func(t *testing.T) {
		input := []byte(`{"cwd":"/other/project"}`)
		result, changed, err := rewrite.SessionFile(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.False(t, changed)

		var decoded map[string]interface{}
		require.NoError(t, json.Unmarshal(result, &decoded))
		assert.Equal(t, "/other/project", decoded["cwd"])
	})

	t.Run("returns error on invalid JSON", func(t *testing.T) {
		_, _, err := rewrite.SessionFile([]byte(`not json`), "/old", "/new")
		assert.Error(t, err)
	})

	t.Run("rewrites occurrences embedded outside the cwd field", assertSessionFileRewritesEmbedded)

	t.Run("does not rewrite a path that is a prefix of another path", func(t *testing.T) {
		input := []byte(`{"cwd":"/old/project-extras"}`)
		result, changed, err := rewrite.SessionFile(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Contains(t, string(result), "/old/project-extras",
			"path-boundary protection must skip prefix collision")
		assert.NotContains(t, string(result), "/new/project-extras")
	})
}

func assertSessionFileRewritesEmbedded(t *testing.T) {
	input := []byte(
		`{"cwd":"/old/project","history":["/old/project/main.go"],` +
			`"notes":"opened /old/project/README.md today"}`,
	)
	result, changed, err := rewrite.SessionFile(input, "/old/project", "/new/project")
	require.NoError(t, err)
	assert.True(t, changed)

	assert.NotContains(t, string(result), `"/old/project"`,
		"quoted cwd occurrence must be rewritten")
	assert.NotContains(t, string(result), "/old/project/",
		"path-followed-by-/ occurrences must be rewritten")
	assert.Contains(t, string(result), "/new/project")
	assert.Contains(t, string(result), "/new/project/main.go")
	assert.Contains(t, string(result), "/new/project/README.md")
}

func TestUserConfig(t *testing.T) {
	t.Run("re-keys old project to new project and preserves other projects", func(t *testing.T) {
		input := []byte(`{
			"projects": {
				"/old/project": {"setting": "value"},
				"/other/project": {"setting": "other-value"}
			},
			"globalSetting": "global"
		}`)

		result, changed, err := rewrite.UserConfig(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.True(t, changed)

		var decoded map[string]interface{}
		require.NoError(t, json.Unmarshal(result, &decoded))

		projects := decoded["projects"].(map[string]interface{})
		assert.Contains(t, projects, "/new/project")
		assert.NotContains(t, projects, "/old/project")
		assert.Contains(t, projects, "/other/project")

		newProjectData := projects["/new/project"].(map[string]interface{})
		assert.Equal(t, "value", newProjectData["setting"])

		assert.Equal(t, "global", decoded["globalSetting"])
	})

	t.Run("returns false when old project key does not exist", func(t *testing.T) {
		input := []byte(`{"projects": {"/other/project": {}}}`)
		_, changed, err := rewrite.UserConfig(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.False(t, changed)
	})

	t.Run("returns error on invalid JSON", func(t *testing.T) {
		_, _, err := rewrite.UserConfig([]byte(`not json`), "/old", "/new")
		assert.Error(t, err)
	})

	t.Run("rewrites embedded path references inside the moved block", assertUserConfigRewritesEmbeddedPaths)

	t.Run("does not rewrite path prefixes inside the moved block", func(t *testing.T) {
		input := []byte(`{
			"projects": {
				"/old/project": {
					"mcpContextUris": ["/old/project-extras/note.md"]
				}
			}
		}`)

		result, changed, err := rewrite.UserConfig(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.True(t, changed)

		assert.Contains(t, string(result), "/old/project-extras/note.md",
			"path-boundary protection must skip prefix collision")
		assert.NotContains(t, string(result), "/new/project-extras")
	})
}

func assertUserConfigRewritesEmbeddedPaths(t *testing.T) {
	input := []byte(`{
		"projects": {
			"/old/project": {
				"mcpServers": {
					"example": {
						"args": ["--root", "/old/project/src"],
						"env": {"PROJECT_DIR": "/old/project"}
					}
				},
				"mcpContextUris": ["file:///old/project/context.md"],
				"exampleFiles": ["/old/project/examples/one.txt"]
			}
		}
	}`)

	result, changed, err := rewrite.UserConfig(input, "/old/project", "/new/project")
	require.NoError(t, err)
	assert.True(t, changed)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(result, &decoded))

	projects := decoded["projects"].(map[string]interface{})
	block := projects["/new/project"].(map[string]interface{})

	mcpServers := block["mcpServers"].(map[string]interface{})
	example := mcpServers["example"].(map[string]interface{})
	args := example["args"].([]interface{})
	assert.Equal(t, "/new/project/src", args[1])

	env := example["env"].(map[string]interface{})
	assert.Equal(t, "/new/project", env["PROJECT_DIR"])

	contextURIs := block["mcpContextUris"].([]interface{})
	assert.Equal(t, "file:///new/project/context.md", contextURIs[0])

	exampleFiles := block["exampleFiles"].([]interface{})
	assert.Equal(t, "/new/project/examples/one.txt", exampleFiles[0])

	assert.NotContains(t, string(result), "/old/project")
}

func TestRewriteSettingsJSON(t *testing.T) {
	t.Run("replaces project path strings in settings content", func(t *testing.T) {
		input := []byte(`{"allowedPaths":["/old/project","/old/project/subdir"],"other":"value"}`)
		result, count := rewrite.ReplaceInBytes(input, "/old/project", "/new/project")
		assert.Equal(t, 2, count)
		assert.Contains(t, string(result), "/new/project")
		assert.NotContains(t, string(result), "/old/project")
	})

	t.Run("returns zero count when no matches in settings content", func(t *testing.T) {
		input := []byte(`{"allowedPaths":["/other/project"]}`)
		result, count := rewrite.ReplaceInBytes(input, "/old/project", "/new/project")
		assert.Equal(t, 0, count)
		assert.Equal(t, input, result)
	})
}

func TestSafeWriteFile(t *testing.T) {
	t.Run("writes data to target path with correct content and permissions", func(t *testing.T) {
		temporaryDirectory := t.TempDir()
		targetPath := filepath.Join(temporaryDirectory, "output.json")
		data := []byte(`{"key": "value"}`)
		permissions := os.FileMode(0600)

		err := rewrite.SafeWriteFile(targetPath, data, permissions)
		require.NoError(t, err)

		written, err := os.ReadFile(targetPath) //nolint:gosec // G304: path is constructed from t.TempDir()
		require.NoError(t, err)
		assert.Equal(t, data, written)

		info, err := os.Stat(targetPath)
		require.NoError(t, err)
		assert.Equal(t, permissions, info.Mode().Perm())
	})

	t.Run("overwrites existing file with new content", func(t *testing.T) {
		temporaryDirectory := t.TempDir()
		targetPath := filepath.Join(temporaryDirectory, "output.json")

		require.NoError(t, os.WriteFile(targetPath, []byte("old content"), 0600))

		newData := []byte("new content")
		require.NoError(t, rewrite.SafeWriteFile(targetPath, newData, 0600))

		written, err := os.ReadFile(targetPath) //nolint:gosec // G304
		require.NoError(t, err)
		assert.Equal(t, newData, written)
	})

	t.Run("returns error when directory does not exist", func(t *testing.T) {
		err := rewrite.SafeWriteFile("/nonexistent/directory/file.json", []byte("data"), 0600)
		assert.Error(t, err)
	})
}
