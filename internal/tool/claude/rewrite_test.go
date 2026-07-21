package claude_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool/claude"
)

func splitLines(data []byte) []string {
	parts := bytes.Split(data, []byte("\n"))
	result := make([]string, len(parts))
	for index, part := range parts {
		result[index] = string(part)
	}
	return result
}

// runStreamHistoryJSONL invokes StreamHistoryJSONL on input, rewriting
// "/old/project" to "/new/project". Keeps each test focused on behavior
// rather than reader/writer plumbing.
func runStreamHistoryJSONL(t *testing.T, input string) (written []byte, count int, malformed []int) {
	t.Helper()
	var dst bytes.Buffer
	replaced, malformed, err := claude.StreamHistoryJSONL(
		t.Context(), strings.NewReader(input), &dst, "/old/project", "/new/project",
	)
	require.NoError(t, err)
	return dst.Bytes(), replaced, malformed
}

func TestStreamHistoryJSONL(t *testing.T) {
	t.Run("rewrites matching lines and preserves non-matching lines", assertStreamHistoryJSONLRewritesMatching)

	t.Run("returns zero count when no lines match", func(t *testing.T) {
		input := `{"project":"/other/project","command":"ls"}` + "\n"

		_, count, malformed := runStreamHistoryJSONL(t, input)

		assert.Equal(t, 0, count)
		assert.Empty(t, malformed)
	})

	t.Run("rewrites path occurrences inside non-project fields", func(t *testing.T) {
		input := `{"project":"/old/project","display":"open /old/project/main.go please"}` + "\n"

		result, count, malformed := runStreamHistoryJSONL(t, input)

		assert.Equal(t, 1, count)
		assert.Empty(t, malformed)
		assert.NotContains(t, string(result), "/old/project")
		assert.Contains(t, string(result), "/new/project/main.go")
	})

	t.Run("does not rewrite a path that is a prefix of another path", func(t *testing.T) {
		input := `{"project":"/old/project-extras","display":"unrelated"}` + "\n"

		result, count, malformed := runStreamHistoryJSONL(t, input)

		assert.Equal(t, 0, count, "path-boundary protection must skip prefix collision")
		assert.Empty(t, malformed)
		assert.Contains(t, string(result), "/old/project-extras")
		assert.NotContains(t, string(result), "/new/project-extras")
	})

	t.Run("preserves the absence of a trailing newline", func(t *testing.T) {
		input := `{"project":"/old/project"}`

		result, _, _ := runStreamHistoryJSONL(t, input)

		assert.False(t, bytes.HasSuffix(result, []byte("\n")),
			"output must not invent a trailing newline that was not in the input")
	})

	t.Run("preserves the presence of a trailing newline", func(t *testing.T) {
		input := `{"project":"/old/project"}` + "\n"

		result, _, _ := runStreamHistoryJSONL(t, input)

		assert.True(t, bytes.HasSuffix(result, []byte("\n")),
			"output must keep the trailing newline present in the input")
	})
}

func TestStreamHistoryJSONL_RewritesAndReportsMalformed(t *testing.T) {
	input := `{"project":"/old","display":"x"}
not-valid-json
{"project":"/old/sub","display":"y"}
`
	var dst bytes.Buffer

	replaced, malformed, err := claude.StreamHistoryJSONL(
		t.Context(), strings.NewReader(input), &dst, "/old", "/new",
	)

	require.NoError(t, err)
	assert.Equal(t, 2, replaced)
	assert.Equal(t, []int{2}, malformed)
	assert.Contains(t, dst.String(), `"project":"/new"`)
	assert.Contains(t, dst.String(), `"project":"/new/sub"`)
	assert.Contains(t, dst.String(), `not-valid-json`)
}

func TestStreamHistoryJSONL_CancelMidStream(t *testing.T) {
	input := strings.Repeat(`{"project":"/old"}`+"\n", 10_000)
	var dst bytes.Buffer

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := claude.StreamHistoryJSONL(ctx, strings.NewReader(input), &dst, "/old", "/new")

	require.ErrorIs(t, err, context.Canceled)
}

func assertStreamHistoryJSONLRewritesMatching(t *testing.T) {
	line1 := `{"project":"/old/project","command":"ls"}`
	line2 := `{"project":"/other/project","command":"pwd"}`
	line3 := `{"project":"/old/project","command":"git status"}`
	input := line1 + "\n" + line2 + "\n" + line3 + "\n"

	result, count, malformed := runStreamHistoryJSONL(t, input)

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

func TestStreamHistoryJSONL_PreservesMalformedLinesVerbatim(t *testing.T) {
	good := `{"project":"/old/project","display":"a"}`
	bad := `{ this is not valid json`
	alsoGood := `{"project":"/old/project","display":"b"}`
	input := good + "\n" + bad + "\n" + alsoGood + "\n"

	result, count, malformed := runStreamHistoryJSONL(t, input)

	assert.Equal(t, 2, count, "two well-formed lines should be rewritten")
	assert.Equal(t, []int{2}, malformed, "1-based line number of the malformed line")

	lines := splitLines(result)
	assert.Equal(t, bad, lines[1], "malformed line must be preserved verbatim")
	assert.Contains(t, lines[0], "/new/project")
	assert.Contains(t, lines[2], "/new/project")
}

func TestStreamHistoryJSONL_RewritesEscapedSlashForm(t *testing.T) {
	input := `{"project":"\/Users\/me\/foo","display":"x"}`
	var dst bytes.Buffer
	replaced, _, err := claude.StreamHistoryJSONL(
		t.Context(), strings.NewReader(input), &dst, "/Users/me/foo", "/Users/me/bar",
	)
	require.NoError(t, err)
	assert.Equal(t, 1, replaced)
	assert.Contains(t, dst.String(), `"project":"\/Users\/me\/bar"`)
}

func TestSessionFile_RewritesEscapedSlashForm(t *testing.T) {
	input := []byte(`{"cwd":"\/Users\/me\/foo","v":1}`)
	got, changed, err := claude.RewriteSessionFile(input, "/Users/me/foo", "/Users/me/bar")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Contains(t, string(got), `"cwd":"\/Users\/me\/bar"`)
}

func TestSessionFile(t *testing.T) {
	t.Run("rewrites cwd when it starts with oldProject", func(t *testing.T) {
		input := []byte(`{"cwd":"/old/project/subdir","extraField":"value"}`)
		result, changed, err := claude.RewriteSessionFile(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.True(t, changed)

		var decoded map[string]interface{}
		require.NoError(t, json.Unmarshal(result, &decoded))
		assert.Equal(t, "/new/project/subdir", decoded["cwd"])
		assert.Equal(t, "value", decoded["extraField"])
	})

	t.Run("rewrites cwd when it equals oldProject exactly", func(t *testing.T) {
		input := []byte(`{"cwd":"/old/project"}`)
		result, changed, err := claude.RewriteSessionFile(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.True(t, changed)

		var decoded map[string]interface{}
		require.NoError(t, json.Unmarshal(result, &decoded))
		assert.Equal(t, "/new/project", decoded["cwd"])
	})

	t.Run("does not rewrite cwd when it does not match oldProject", func(t *testing.T) {
		input := []byte(`{"cwd":"/other/project"}`)
		result, changed, err := claude.RewriteSessionFile(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.False(t, changed)

		var decoded map[string]interface{}
		require.NoError(t, json.Unmarshal(result, &decoded))
		assert.Equal(t, "/other/project", decoded["cwd"])
	})

	t.Run("returns error on invalid JSON", func(t *testing.T) {
		_, _, err := claude.RewriteSessionFile([]byte(`not json`), "/old", "/new")
		assert.Error(t, err)
	})

	t.Run("rewrites occurrences embedded outside the cwd field", assertSessionFileRewritesEmbedded)

	t.Run("does not rewrite a path that is a prefix of another path", func(t *testing.T) {
		input := []byte(`{"cwd":"/old/project-extras"}`)
		result, changed, err := claude.RewriteSessionFile(input, "/old/project", "/new/project")
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
	result, changed, err := claude.RewriteSessionFile(input, "/old/project", "/new/project")
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

		result, changed, err := claude.RewriteUserConfig(input, "/old/project", "/new/project")
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
		_, changed, err := claude.RewriteUserConfig(input, "/old/project", "/new/project")
		require.NoError(t, err)
		assert.False(t, changed)
	})

	t.Run("returns error on invalid JSON", func(t *testing.T) {
		_, _, err := claude.RewriteUserConfig([]byte(`not json`), "/old", "/new")
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

		result, changed, err := claude.RewriteUserConfig(input, "/old/project", "/new/project")
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

	result, changed, err := claude.RewriteUserConfig(input, "/old/project", "/new/project")
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

	result, changed, err := claude.RewriteUserConfig(input, "/old/project", "/new/project")
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
