package rewrite_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestHistoryJSONL(t *testing.T) {
	t.Run("rewrites matching lines and preserves non-matching lines", assertHistoryJSONLRewritesMatching)

	t.Run("returns zero count when no lines match", func(t *testing.T) {
		input := []byte(`{"project":"/other/project","command":"ls"}` + "\n")
		_, count, malformed, err := rewrite.HistoryJSONL(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.Equal(t, 0, count)
		assert.Empty(t, malformed)
	})

	t.Run("preserves malformed JSON lines verbatim and reports their line numbers", func(t *testing.T) {
		good := `{"project":"/old/project","display":"a"}`
		bad := `{ this is not valid json`
		alsoGood := `{"project":"/old/project","display":"b"}`
		input := []byte(good + "\n" + bad + "\n" + alsoGood + "\n")

		result, count, malformed, err := rewrite.HistoryJSONL(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.Equal(t, 2, count, "two well-formed lines should be rewritten")
		assert.Equal(t, []int{2}, malformed, "1-based line number of the malformed line")

		lines := splitLines(result)
		assert.Equal(t, bad, lines[1], "malformed line must be preserved verbatim")
		assert.Contains(t, lines[0], "/new/project")
		assert.Contains(t, lines[2], "/new/project")
	})

	t.Run("rewrites path occurrences inside non-project fields", func(t *testing.T) {
		input := []byte(`{"project":"/old/project","display":"open /old/project/main.go please"}` + "\n")
		result, count, malformed, err := rewrite.HistoryJSONL(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.Equal(t, 1, count)
		assert.Empty(t, malformed)
		assert.NotContains(t, string(result), "/old/project")
		assert.Contains(t, string(result), "/new/project/main.go")
	})

	t.Run("does not rewrite a path that is a prefix of another path", func(t *testing.T) {
		input := []byte(`{"project":"/old/project-extras","display":"unrelated"}` + "\n")
		result, count, malformed, err := rewrite.HistoryJSONL(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.Equal(t, 0, count, "path-boundary protection must skip prefix collision")
		assert.Empty(t, malformed)
		assert.Contains(t, string(result), "/old/project-extras")
		assert.NotContains(t, string(result), "/new/project-extras")
	})
}

func assertHistoryJSONLRewritesMatching(t *testing.T) {
	line1 := `{"project":"/old/project","command":"ls"}`
	line2 := `{"project":"/other/project","command":"pwd"}`
	line3 := `{"project":"/old/project","command":"git status"}`
	input := []byte(line1 + "\n" + line2 + "\n" + line3 + "\n")

	result, count, malformed, err := rewrite.HistoryJSONL(input, "/old/project", "/new/project")
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.Empty(t, malformed)

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

	t.Run("preserves top-level key order and formatting outside the edit", assertUserConfigPreservesFormatting)

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

func assertUserConfigPreservesFormatting(t *testing.T) {
	// Input uses 2-space indent and a specific top-level key order that Go's
	// encoding/json would scramble (alphabetical). sjson-based splicing must
	// leave everything outside the rekeyed projects entry byte-identical.
	input := []byte(`{
  "numStartups": 42,
  "theme": "dark",
  "projects": {
    "/old/project": {"setting": "value"},
    "/other/project": {"setting": "other-value"}
  },
  "oauthAccount": {
    "email": "user@example.com"
  }
}
`)

	result, changed, err := rewrite.UserConfig(input, "/old/project", "/new/project")
	require.NoError(t, err)
	assert.True(t, changed)

	assert.Equal(t,
		[]string{"numStartups", "theme", "projects", "oauthAccount"},
		topLevelKeys(t, result),
		"top-level key order must survive the rewrite",
	)

	// Lines outside the projects object must be preserved byte-for-byte —
	// including leading indent, which Go's encoding/json would otherwise
	// collapse or reorder.
	content := string(result)
	assert.Contains(t, content, "\n  \"numStartups\": 42,\n",
		"numStartups line and its indent must survive")
	assert.Contains(t, content, "\n  \"theme\": \"dark\",\n",
		"theme line and its indent must survive")
	assert.Contains(t, content, "\n    \"email\": \"user@example.com\"\n",
		"oauthAccount.email line and its 4-space indent must survive")
	assert.True(t, strings.HasSuffix(content, "}\n"),
		"trailing newline must survive")
	assert.Contains(t, content, "\"/other/project\": {\"setting\": \"other-value\"}",
		"unaffected project key must be preserved byte-for-byte")
}

// topLevelKeys parses raw as a JSON object and returns its keys in the order
// they appear on the wire, using a token stream rather than decoding to a map
// (which Go deliberately randomizes).
func topLevelKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	startToken, err := decoder.Token()
	require.NoError(t, err)
	require.Equal(t, json.Delim('{'), startToken, "expected a top-level object")

	var keys []string
	for decoder.More() {
		keyToken, err := decoder.Token()
		require.NoError(t, err)
		keys = append(keys, keyToken.(string))
		var skip json.RawMessage
		require.NoError(t, decoder.Decode(&skip))
	}
	return keys
}

func TestRewriteSettingsJSON(t *testing.T) {
	t.Run("replaces project path strings in settings content", func(t *testing.T) {
		input := []byte(`{"allowedPaths":["/old/project","/old/project/subdir"],"other":"value"}`)
		result, count := rewrite.ReplacePathInBytes(input, "/old/project", "/new/project")
		assert.Equal(t, 2, count)
		assert.Contains(t, string(result), "/new/project")
		assert.NotContains(t, string(result), "/old/project")
	})

	t.Run("returns zero count when no matches in settings content", func(t *testing.T) {
		input := []byte(`{"allowedPaths":["/other/project"]}`)
		result, count := rewrite.ReplacePathInBytes(input, "/old/project", "/new/project")
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

func TestFindPlaceholderTokens(t *testing.T) {
	t.Run("finds distinct upper-snake tokens", func(t *testing.T) {
		data := []byte(`cwd={{PROJECT_PATH}}, home={{HOME}}, extra={{UNRESOLVED_1}}`)
		tokens := rewrite.FindPlaceholderTokens(data)
		assert.Equal(t, []string{"{{PROJECT_PATH}}", "{{HOME}}", "{{UNRESOLVED_1}}"}, tokens)
	})

	t.Run("deduplicates repeated occurrences", func(t *testing.T) {
		data := []byte(`{{KEY}} {{KEY}} and {{KEY}} again`)
		assert.Equal(t, []string{"{{KEY}}"}, rewrite.FindPlaceholderTokens(data))
	})

	t.Run("ignores whitespace inside braces", func(t *testing.T) {
		assert.Empty(t, rewrite.FindPlaceholderTokens([]byte(`{{ }}`)))
		assert.Empty(t, rewrite.FindPlaceholderTokens([]byte(`{{ KEY }}`)))
	})

	t.Run("ignores lowercase keys", func(t *testing.T) {
		assert.Empty(t, rewrite.FindPlaceholderTokens([]byte(`{{lower}}`)))
		assert.Empty(t, rewrite.FindPlaceholderTokens([]byte(`{{MixedCase}}`)))
	})

	t.Run("ignores empty braces and unclosed sequences", func(t *testing.T) {
		assert.Empty(t, rewrite.FindPlaceholderTokens([]byte(`{{}}`)))
		assert.Empty(t, rewrite.FindPlaceholderTokens([]byte(`{{KEY`)))
		assert.Empty(t, rewrite.FindPlaceholderTokens([]byte(`{{KEY}`)))
	})

	t.Run("requires close immediately after key bytes", func(t *testing.T) {
		// `{{KEY!}}` has a non-key byte before the close — rejected.
		assert.Empty(t, rewrite.FindPlaceholderTokens([]byte(`{{KEY!}}`)))
	})

	t.Run("bounds key length to avoid pathological scans", func(t *testing.T) {
		// 65 A's followed by }} — beyond the 64-byte cap, so not accepted.
		long := append([]byte(`{{`), bytes.Repeat([]byte("A"), 65)...)
		long = append(long, []byte(`}}`)...)
		assert.Empty(t, rewrite.FindPlaceholderTokens(long))
	})

	t.Run("handles adjacent tokens", func(t *testing.T) {
		data := []byte(`{{A}}{{B}}`)
		assert.Equal(t, []string{"{{A}}", "{{B}}"}, rewrite.FindPlaceholderTokens(data))
	})

	t.Run("handles token-before-text-before-token", func(t *testing.T) {
		data := []byte(`{{A}} middle {{B}}`)
		assert.Equal(t, []string{"{{A}}", "{{B}}"}, rewrite.FindPlaceholderTokens(data))
	})

	t.Run("returns nil on empty input", func(t *testing.T) {
		assert.Empty(t, rewrite.FindPlaceholderTokens(nil))
		assert.Empty(t, rewrite.FindPlaceholderTokens([]byte{}))
	})
}

func TestSafeRenamePromoter_Files(t *testing.T) {
	t.Run("promotes a file onto a non-existent final", func(t *testing.T) {
		dir := t.TempDir()
		final := filepath.Join(dir, "final.txt")
		temp := filepath.Join(dir, "final.txt.tmp")
		require.NoError(t, os.WriteFile(temp, []byte("staged"), 0600))

		promoter := rewrite.NewSafeRenamePromoter()
		promoter.StageFile(temp, final)
		require.NoError(t, promoter.Promote())

		data, err := os.ReadFile(final) //nolint:gosec // G304: t.TempDir() path
		require.NoError(t, err)
		assert.Equal(t, "staged", string(data))
		assert.NoFileExists(t, temp)
	})

	t.Run("promotes a file over an existing final", func(t *testing.T) {
		dir := t.TempDir()
		final := filepath.Join(dir, "final.txt")
		temp := filepath.Join(dir, "final.txt.tmp")
		require.NoError(t, os.WriteFile(final, []byte("old"), 0600))
		require.NoError(t, os.WriteFile(temp, []byte("new"), 0600))

		promoter := rewrite.NewSafeRenamePromoter()
		promoter.StageFile(temp, final)
		require.NoError(t, promoter.Promote())

		data, err := os.ReadFile(final) //nolint:gosec // G304: t.TempDir() path
		require.NoError(t, err)
		assert.Equal(t, "new", string(data))
	})

	t.Run("rollback restores the pre-promote contents of an existing final", assertRollbackRestoresFile)
	t.Run("rollback removes a promoted file that did not exist before", assertRollbackRemovesNewFile)
}

func TestSafeRenamePromoter_Dirs(t *testing.T) {
	t.Run("promotes a directory onto a non-existent final", func(t *testing.T) {
		dir := t.TempDir()
		final := filepath.Join(dir, "project")
		temp := filepath.Join(dir, "project.tmp")
		require.NoError(t, os.MkdirAll(filepath.Join(temp, "sub"), 0750))
		require.NoError(t, os.WriteFile(filepath.Join(temp, "sub", "x.txt"), []byte("x"), 0600))

		promoter := rewrite.NewSafeRenamePromoter()
		promoter.StageDir(temp, final)
		require.NoError(t, promoter.Promote())

		data, err := os.ReadFile(filepath.Join(final, "sub", "x.txt")) //nolint:gosec // G304: t.TempDir() path
		require.NoError(t, err)
		assert.Equal(t, "x", string(data))
		assert.NoDirExists(t, temp)
	})

	t.Run("rollback restores an overwritten directory", assertRollbackRestoresDir)
}

func assertRollbackRestoresFile(t *testing.T) {
	dir := t.TempDir()
	finalA := filepath.Join(dir, "a.txt")
	tempA := filepath.Join(dir, "a.txt.tmp")
	finalB := filepath.Join(dir, "b.txt")
	tempB := filepath.Join(dir, "b.txt.tmp")

	require.NoError(t, os.WriteFile(finalA, []byte("A-old"), 0600))
	require.NoError(t, os.WriteFile(finalB, []byte("B-old"), 0600))
	require.NoError(t, os.WriteFile(tempA, []byte("A-new"), 0600))
	require.NoError(t, os.WriteFile(tempB, []byte("B-new"), 0600))

	promoter := rewrite.NewSafeRenamePromoter()
	promoter.StageFile(tempA, finalA)
	promoter.StageFile(tempB, finalB)
	promoter.SetRenameFunc(failOnCallN(2))

	err := promoter.Promote()
	require.Error(t, err)

	got, readErr := os.ReadFile(finalA) //nolint:gosec // G304: t.TempDir() path
	require.NoError(t, readErr)
	assert.Equal(t, "A-old", string(got))

	got, readErr = os.ReadFile(finalB) //nolint:gosec // G304: t.TempDir() path
	require.NoError(t, readErr)
	assert.Equal(t, "B-old", string(got))
}

func assertRollbackRemovesNewFile(t *testing.T) {
	dir := t.TempDir()
	finalA := filepath.Join(dir, "a.txt")
	tempA := filepath.Join(dir, "a.txt.tmp")
	finalB := filepath.Join(dir, "b.txt")
	tempB := filepath.Join(dir, "b.txt.tmp")
	require.NoError(t, os.WriteFile(tempA, []byte("A-new"), 0600))
	require.NoError(t, os.WriteFile(tempB, []byte("B-new"), 0600))

	promoter := rewrite.NewSafeRenamePromoter()
	promoter.StageFile(tempA, finalA)
	promoter.StageFile(tempB, finalB)
	promoter.SetRenameFunc(failOnCallN(2))

	err := promoter.Promote()
	require.Error(t, err)

	assert.NoFileExists(t, finalA)
	assert.NoFileExists(t, finalB)
}

func assertRollbackRestoresDir(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "project")
	temp := filepath.Join(dir, "project.tmp")

	require.NoError(t, os.MkdirAll(final, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(final, "old.txt"), []byte("old"), 0600))
	require.NoError(t, os.MkdirAll(temp, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(temp, "new.txt"), []byte("new"), 0600))

	other := filepath.Join(dir, "other.txt")
	otherTemp := filepath.Join(dir, "other.txt.tmp")
	require.NoError(t, os.WriteFile(otherTemp, []byte("o"), 0600))

	promoter := rewrite.NewSafeRenamePromoter()
	promoter.StageDir(temp, final)
	promoter.StageFile(otherTemp, other)
	// call 1: stash dir backup; 2: promote dir; 3: promote file (fail).
	promoter.SetRenameFunc(failOnCallN(3))

	err := promoter.Promote()
	require.Error(t, err)

	got, readErr := os.ReadFile(filepath.Join(final, "old.txt")) //nolint:gosec // G304: t.TempDir() path
	require.NoError(t, readErr)
	assert.Equal(t, "old", string(got))
	assert.NoFileExists(t, filepath.Join(final, "new.txt"))
}

// failOnCallN returns a rename hook that invokes os.Rename on every call
// except the nth, where it returns a simulated failure. Centralises the
// "fail on call N" pattern shared by the rollback sub-tests.
func failOnCallN(n int) func(oldpath, newpath string) error {
	callCount := 0
	return func(oldpath, newpath string) error {
		callCount++
		if callCount == n {
			return errors.New("simulated failure")
		}
		return os.Rename(oldpath, newpath)
	}
}

func TestEscapeSJSONKey(t *testing.T) {
	t.Run("escapes dots so they are not read as nested keys", func(t *testing.T) {
		assert.Equal(t, `/Users/x/proj\.v2`, rewrite.EscapeSJSONKey("/Users/x/proj.v2"))
	})

	t.Run("escapes backslashes before dots", func(t *testing.T) {
		// Order matters: backslash escape must run before dot escape, otherwise
		// the backslash inserted by dot-escaping would be doubled a second time.
		assert.Equal(t, `a\\b\.c`, rewrite.EscapeSJSONKey(`a\b.c`))
	})

	t.Run("leaves keys without metacharacters untouched", func(t *testing.T) {
		assert.Equal(t, "/plain/key", rewrite.EscapeSJSONKey("/plain/key"))
	})

	t.Run("handles empty input", func(t *testing.T) {
		assert.Empty(t, rewrite.EscapeSJSONKey(""))
	})
}

func TestIsLikelyText(t *testing.T) {
	t.Run("empty buffer is treated as text", func(t *testing.T) {
		assert.True(t, rewrite.IsLikelyText(nil))
		assert.True(t, rewrite.IsLikelyText([]byte{}))
	})

	t.Run("plain ASCII text", func(t *testing.T) {
		assert.True(t, rewrite.IsLikelyText([]byte("hello /Users/x/project world")))
	})

	t.Run("null byte in first window is detected", func(t *testing.T) {
		data := append([]byte{0x00}, bytes.Repeat([]byte("a"), 4096)...)
		assert.False(t, rewrite.IsLikelyText(data))
	})

	t.Run("null byte only in middle window is detected", func(t *testing.T) {
		// 4096 bytes of text, then a null, then 4096 bytes of text.
		// Head window is clean; tail window is clean; middle window must fire.
		data := bytes.Repeat([]byte("a"), 4096)
		data = append(data, 0x00)
		data = append(data, bytes.Repeat([]byte("b"), 4096)...)
		assert.False(t, rewrite.IsLikelyText(data),
			"middle-window null must be detected by triple-window heuristic")
	})

	t.Run("null byte only in tail window is detected", func(t *testing.T) {
		data := bytes.Repeat([]byte("a"), 4096)
		// Place null within the last 512 bytes but keep the middle clean.
		data = append(data, bytes.Repeat([]byte("b"), 1000)...)
		data = append(data, 0x00)
		data = append(data, bytes.Repeat([]byte("c"), 50)...)
		assert.False(t, rewrite.IsLikelyText(data),
			"tail-window null must be detected by triple-window heuristic")
	})

	t.Run("short buffer uses single window", func(t *testing.T) {
		assert.True(t, rewrite.IsLikelyText([]byte("short text no nulls")))
		assert.False(t, rewrite.IsLikelyText([]byte("short\x00text")))
	})

	t.Run("magic-byte prefixes classify as binary regardless of nulls", func(t *testing.T) {
		cases := map[string][]byte{
			"PNG":  {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
			"JPEG": {0xff, 0xd8, 0xff, 0xe0},
			"PDF":  []byte("%PDF-1.7\nhello world plain ASCII tail no nulls"),
			"ZIP":  {'P', 'K', 0x03, 0x04, 'h', 'e', 'l', 'l', 'o'},
			"gzip": {0x1f, 0x8b, 'h', 'i'},
		}
		for name, magic := range cases {
			// Pad with text to prove the decision is made by magic bytes,
			// not by the null-byte scan that would otherwise pass.
			data := append([]byte{}, magic...)
			data = append(data, bytes.Repeat([]byte("a"), 2048)...)
			assert.False(t, rewrite.IsLikelyText(data),
				"%s-prefixed buffer must be classified as binary", name)
		}
	})

	t.Run("non-magic byte sequence resembling PNG header is still text", func(t *testing.T) {
		// UTF-8 text that happens to begin with 0x89 0x50 but is not the
		// full PNG magic (0x89 'P' 'N' 'G' '\r' '\n' 0x1a '\n'). Must not
		// false-positive as binary.
		data := append([]byte{0x89, 'P'}, []byte(" some text ")...)
		assert.True(t, rewrite.IsLikelyText(data))
	})
}
