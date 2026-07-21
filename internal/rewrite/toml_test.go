package rewrite_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/rewrite"
)

func TestTOMLPathRewriteRenamesProjectKeysAndValues(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "project table key",
			input: "[projects.\"/Users/test/Projects/my_app\"]\ntrusted = true\n",
			want:  "[projects.\"/Users/test/Projects/new_app\"]\ntrusted = true\n",
		},
		{
			name:  "project path value",
			input: "[hooks]\nstate = \"/Users/test/Projects/my_app/hooks.json\"\n",
			want:  "[hooks]\nstate = \"/Users/test/Projects/new_app/hooks.json\"\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rewritten, count, err := rewrite.TOMLPathRewrite([]byte(testCase.input), "/Users/test/Projects/my_app", "/Users/test/Projects/new_app")

			require.NoError(t, err)
			assert.Equal(t, 1, count)
			assert.Equal(t, testCase.want, string(rewritten))
		})
	}
}

func TestTOMLPathRewritePreservesCommentsAndFormatting(t *testing.T) {
	input := "# keep this comment and spacing\n" +
		"[projects.\"/Users/test/Projects/my_app\"] # table comment\n" +
		"trusted = true # value comment\n\n" +
		"[hooks]\nstate = \"/Users/test/Projects/my_app/hooks.json\"\n"
	want := "# keep this comment and spacing\n" +
		"[projects.\"/Users/test/Projects/new_app\"] # table comment\n" +
		"trusted = true # value comment\n\n" +
		"[hooks]\nstate = \"/Users/test/Projects/new_app/hooks.json\"\n"

	rewritten, count, err := rewrite.TOMLPathRewrite([]byte(input), "/Users/test/Projects/my_app", "/Users/test/Projects/new_app")

	require.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.Equal(t, want, string(rewritten))
}

func TestTOMLPathRewriteRewritesProjectLocalHookStateKey(t *testing.T) {
	oldPath := "/Users/test/Projects/my_app"
	newPath := "/Users/test/Projects/new_app"
	input := "[projects.\"" + oldPath + "\"]\ntrust_level = \"trusted\"\n\n" +
		"[hooks.state.\"" + oldPath + "/.codex/hooks.toml:pre_tool_use:0:0\"]\n" +
		"enabled = true\ntrusted_hash = \"abc123\"\n"
	want := "[projects.\"" + newPath + "\"]\ntrust_level = \"trusted\"\n\n" +
		"[hooks.state.\"" + newPath + "/.codex/hooks.toml:pre_tool_use:0:0\"]\n" +
		"enabled = true\ntrusted_hash = \"abc123\"\n"

	rewritten, count, err := rewrite.TOMLPathRewrite([]byte(input), oldPath, newPath)

	require.NoError(t, err)
	assert.Equal(t, 2, count, "the projects key and the project-local hooks.state key both rewrite")
	assert.Equal(t, want, string(rewritten))
}

func TestTOMLPathRewriteLeavesUserLevelHookStateKeyUntouched(t *testing.T) {
	oldPath := "/Users/test/Projects/my_app"
	newPath := "/Users/test/Projects/new_app"
	userHookKey := "/Users/test/.codex/hooks/global.toml:pre_tool_use:0:0"
	input := "[projects.\"" + oldPath + "\"]\ntrust_level = \"trusted\"\n\n" +
		"[hooks.state.\"" + oldPath + "/.codex/hooks.toml:pre_tool_use:0:0\"]\nenabled = true\n\n" +
		"[hooks.state.\"" + userHookKey + "\"]\nenabled = true\n"
	want := "[projects.\"" + newPath + "\"]\ntrust_level = \"trusted\"\n\n" +
		"[hooks.state.\"" + newPath + "/.codex/hooks.toml:pre_tool_use:0:0\"]\nenabled = true\n\n" +
		"[hooks.state.\"" + userHookKey + "\"]\nenabled = true\n"

	rewritten, count, err := rewrite.TOMLPathRewrite([]byte(input), oldPath, newPath)

	require.NoError(t, err)
	assert.Equal(t, 2, count, "the user-level hook key lies outside the moved project and stays put")
	assert.Equal(t, want, string(rewritten))
}

func TestTOMLPathRewriteRewritesPathKeyInsideArrayOfTables(t *testing.T) {
	oldPath := "/Users/test/Projects/my_app"
	newPath := "/Users/test/Projects/new_app"
	input := "[projects.\"" + oldPath + "\"]\ntrusted = true\n\n" +
		"[[audit_log]]\n[audit_log.\"" + oldPath + "\"]\nenabled = true\n"
	want := "[projects.\"" + newPath + "\"]\ntrusted = true\n\n" +
		"[[audit_log]]\n[audit_log.\"" + newPath + "\"]\nenabled = true\n"

	rewritten, count, err := rewrite.TOMLPathRewrite([]byte(input), oldPath, newPath)

	require.NoError(t, err)
	assert.Equal(t, 2, count, "the recursion reaches the path key nested in the array-of-tables element")
	assert.Equal(t, want, string(rewritten))
}

func TestTOMLPathRewriteRefusesCollidingProjectKeyRewrite(t *testing.T) {
	oldPath := "/Users/test/Projects/my_app"
	newPath := "/Users/test/Projects/new_app"
	// The destination key already exists, so rewriting old→new fuses two project
	// entries into one. The rewritten bytes then hold a duplicate table, so the
	// output re-parse refuses before any write; the collision never reaches the
	// key-multiset comparison.
	input := "[projects.\"" + oldPath + "\"]\ntrust_level = \"trusted\"\n\n" +
		"[projects.\"" + newPath + "\"]\ntrust_level = \"trusted\"\n"

	rewritten, count, err := rewrite.TOMLPathRewrite([]byte(input), oldPath, newPath)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "validate TOML path rewrite output",
		"a collision fails the output re-parse, not the key-multiset comparison")
	assert.Zero(t, count)
	assert.Equal(t, input, string(rewritten), "a refused rewrite returns the input unchanged")
}

func TestTOMLPathRewriteRejectsQuotedAndBackslashedPaths(t *testing.T) {
	input := []byte("[projects.\"/Users/test/Projects/my_app\"]\ntrusted = true\n")
	cases := []struct {
		name    string
		oldPath string
		newPath string
	}{
		{"quote", "/Users/test/Projects/my_app", "/Users/test/Projects/new\"app"},
		{"backslash", "/Users/test/Projects/my\\app", "/Users/test/Projects/new_app"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rewritten, count, err := rewrite.TOMLPathRewrite(input, testCase.oldPath, testCase.newPath)

			require.Error(t, err)
			assert.Zero(t, count)
			assert.Equal(t, input, rewritten)
		})
	}
}
