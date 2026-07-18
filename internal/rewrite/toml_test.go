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

func TestTOMLPathRewriteRejectsKeyChangesOutsideProjects(t *testing.T) {
	input := "[projects.\"/Users/test/Projects/my_app\"]\ntrusted = true\n\n[other.\"/Users/test/Projects/my_app\"]\nenabled = true\n"

	rewritten, count, err := rewrite.TOMLPathRewrite([]byte(input), "/Users/test/Projects/my_app", "/Users/test/Projects/new_app")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "key paths changed outside projects")
	assert.Zero(t, count)
	assert.Equal(t, input, string(rewritten))
}

func TestTOMLPathRewriteRejectsKeyChangesInsideArrayOfTables(t *testing.T) {
	input := "[projects.\"/Users/test/Projects/my_app\"]\ntrusted = true\n\n[[audit_log]]\n[audit_log.\"/Users/test/Projects/my_app\"]\nenabled = true\n"

	rewritten, count, err := rewrite.TOMLPathRewrite([]byte(input), "/Users/test/Projects/my_app", "/Users/test/Projects/new_app")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "key paths changed outside projects")
	assert.Zero(t, count)
	assert.Equal(t, input, string(rewritten))
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
