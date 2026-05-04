package export_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
)

func TestDiscoverPaths(t *testing.T) {
	content := []byte(`project at /Users/test/Projects/myproject and home /Users/test with /opt/homebrew/bin/tool`)
	paths := export.DiscoverPaths(content)
	assert.Contains(t, paths, "/Users/test/Projects/myproject")
	assert.Contains(t, paths, "/Users/test")
	assert.Contains(t, paths, "/opt/homebrew/bin/tool")
}

func TestDiscoverPaths_JSON(t *testing.T) {
	content := []byte(`{"cwd":"/Users/test/Projects/myproject","tool":"/usr/local/bin/go","home":"/Users/test"}`)
	paths := export.DiscoverPaths(content)
	assert.Contains(t, paths, "/Users/test/Projects/myproject")
	assert.Contains(t, paths, "/usr/local/bin/go")
	assert.Contains(t, paths, "/Users/test")
}

func TestDiscoverPaths_NoPaths(t *testing.T) {
	content := []byte(`no absolute paths here, just words and numbers like 42 or relative-names`)
	paths := export.DiscoverPaths(content)
	assert.Empty(t, paths)
}

func TestDiscoverPaths_Deduplication(t *testing.T) {
	content := []byte(`/Users/test/Projects/myproject appears twice: /Users/test/Projects/myproject`)
	paths := export.DiscoverPaths(content)

	count := 0
	for _, path := range paths {
		if path == "/Users/test/Projects/myproject" {
			count++
		}
	}
	assert.Equal(t, 1, count, "duplicate paths should be deduplicated")
}

func TestDiscoverPaths_TrailingDotsSlashes(t *testing.T) {
	content := []byte(`path ending with slash /Users/test/ and dot /Users/test.`)
	paths := export.DiscoverPaths(content)

	// Neither the slash-trailing nor the dot-trailing form should appear
	assert.NotContains(t, paths, "/Users/test/")
	assert.NotContains(t, paths, "/Users/test.")
}

func TestGroupPathPrefixes(t *testing.T) {
	paths := []string{
		"/Users/test/Projects/myproject",
		"/Users/test/Projects/otherproject",
		"/Users/test/Documents/notes",
		"/opt/homebrew/bin/tool",
	}
	prefixes := export.GroupPathPrefixes(paths)

	// /Users/test should emerge as a common prefix covering 3 input paths;
	// sub-paths must be absorbed into it, not kept as separate top-level prefixes.
	assert.Contains(t, prefixes, "/Users/test")
	assert.NotContains(t, prefixes, "/Users/test/Projects/myproject")
	assert.NotContains(t, prefixes, "/Users/test/Projects/otherproject")
	assert.NotContains(t, prefixes, "/Users/test/Documents/notes")
}

func TestGroupPathPrefixes_Empty(t *testing.T) {
	prefixes := export.GroupPathPrefixes(nil)
	assert.Nil(t, prefixes)
}

func TestGroupPathPrefixes_SinglePath(t *testing.T) {
	prefixes := export.GroupPathPrefixes([]string{"/Users/test/Projects/myproject"})
	// Single path: no parent has count >= 2, so only the path itself is kept
	assert.Contains(t, prefixes, "/Users/test/Projects/myproject")
}

func TestAutoDetectPlaceholders(t *testing.T) {
	prefixes := []string{
		"/Users/test/Projects/myproject",
		"/Users/test",
		"/opt/homebrew",
	}
	suggestions := export.AutoDetectPlaceholders(prefixes, "/Users/test/Projects/myproject", "/Users/test")

	assert.Len(t, suggestions, 3)

	assert.Equal(t, "{{PROJECT_PATH}}", suggestions[0].Key)
	assert.Equal(t, "/Users/test/Projects/myproject", suggestions[0].Original)
	assert.True(t, suggestions[0].Auto)

	assert.Equal(t, "{{HOME}}", suggestions[1].Key)
	assert.Equal(t, "/Users/test", suggestions[1].Original)
	assert.True(t, suggestions[1].Auto)

	assert.Equal(t, "{{UNRESOLVED_1}}", suggestions[2].Key)
	assert.Equal(t, "/opt/homebrew", suggestions[2].Original)
	assert.False(t, suggestions[2].Auto)
}

func TestAutoDetectPlaceholders_MultipleUnresolved(t *testing.T) {
	prefixes := []string{"/opt/homebrew", "/usr/local", "/var/log"}
	suggestions := export.AutoDetectPlaceholders(prefixes, "/Users/test/Projects/myproject", "/Users/test")

	assert.Len(t, suggestions, 3)
	assert.Equal(t, "{{UNRESOLVED_1}}", suggestions[0].Key)
	assert.Equal(t, "{{UNRESOLVED_2}}", suggestions[1].Key)
	assert.Equal(t, "{{UNRESOLVED_3}}", suggestions[2].Key)
	assert.False(t, suggestions[0].Auto)
	assert.False(t, suggestions[1].Auto)
	assert.False(t, suggestions[2].Auto)
}

func TestAutoDetectPlaceholders_Empty(t *testing.T) {
	suggestions := export.AutoDetectPlaceholders(nil, "/Users/test/project", "/Users/test")
	assert.Empty(t, suggestions)
}

func TestDiscoverPlaceholders_EmitsBothAnchorsLongestFirst(t *testing.T) {
	content := []byte(`session at /Users/tap-user/Software/homebrew-tap/Casks/cc-port.rb edited by /Users/tap-user`)
	suggestions := export.DiscoverPlaceholders(
		content,
		"/Users/tap-user/Software/homebrew-tap",
		"/Users/tap-user",
	)

	require.Len(t, suggestions, 2)
	assert.Equal(t, "{{PROJECT_PATH}}", suggestions[0].Key)
	assert.Equal(t, "/Users/tap-user/Software/homebrew-tap", suggestions[0].Original)
	assert.Equal(t, "{{HOME}}", suggestions[1].Key)
	assert.Equal(t, "/Users/tap-user", suggestions[1].Original)
}

func TestDiscoverPlaceholders_EmitsProjectPathWhenNestedUnderHome(t *testing.T) {
	content := []byte(`opened /Users/tap-user/Software/homebrew-tap/Casks/cc-port.rb from /Users/tap-user`)

	suggestions := export.DiscoverPlaceholders(
		content,
		"/Users/tap-user/Software/homebrew-tap",
		"/Users/tap-user",
	)

	require.Len(t, suggestions, 2, "both anchors must be emitted independently when project is nested under home")
	assert.Equal(t, "{{PROJECT_PATH}}", suggestions[0].Key)
	assert.Greater(t, len(suggestions[0].Original), len(suggestions[1].Original),
		"PROJECT_PATH must be longer than HOME so applyPlaceholders substitutes it first")
}

func TestDiscoverPlaceholders_DropsBase64FromMCPResponses(t *testing.T) {
	base64Blob := "ErQCClkIDRgCKkAaa3B+26BuFyhLQ7qS854Nd/wZ5DGdij31UblA2d0Fy0k+" +
		"Y/+TR/V6WkYEWoxA+Xvy2VeFkuetpDM85L/BFk1XMg9jbGF1ZGUtb3B1cy00" +
		"LTc4ABIMXzD3KtcZLzXjCAaHGgxsD9"
	content := []byte("anchor /Users/tap-user/Software/homebrew-tap and tool result " + base64Blob + " ends here")

	suggestions := export.DiscoverPlaceholders(content, "/Users/tap-user/Software/homebrew-tap", "/Users/tap-user")

	assertOnlyAnchorPlaceholders(t, suggestions, "ErQCClkIDRgCKkA")
}

func TestDiscoverPlaceholders_DropsGitHubURLFragments(t *testing.T) {
	content := []byte(`anchor /Users/tap-user/Software/homebrew-tap and url https://github.com/it-bens/cc-port done`)

	suggestions := export.DiscoverPlaceholders(content, "/Users/tap-user/Software/homebrew-tap", "/Users/tap-user")

	assertOnlyAnchorPlaceholders(t, suggestions, "//github.com/it-bens")
}

func TestDiscoverPlaceholders_DropsGitHubActionRefs(t *testing.T) {
	actionPin := "Homebrew/actions/setup-homebrew@98cfa07b984a61682e6cd3a0833fad2006cc84ba"
	content := []byte("anchor /Users/tap-user/Software/homebrew-tap and pin " + actionPin + " done")

	suggestions := export.DiscoverPlaceholders(content, "/Users/tap-user/Software/homebrew-tap", "/Users/tap-user")

	assertOnlyAnchorPlaceholders(t, suggestions, "/setup-homebrew")
}

func TestDiscoverPlaceholders_DropsRuboCopCopNames(t *testing.T) {
	content := []byte(`anchor /Users/tap-user/Software/homebrew-tap rule Style/IfUnlessModifier triggered`)

	suggestions := export.DiscoverPlaceholders(content, "/Users/tap-user/Software/homebrew-tap", "/Users/tap-user")

	assertOnlyAnchorPlaceholders(t, suggestions, "/IfUnlessModifier")
}

func TestDiscoverPlaceholders_DropsGitRefFragments(t *testing.T) {
	content := []byte(`anchor /Users/tap-user/Software/homebrew-tap then git rebase origin/main done`)

	suggestions := export.DiscoverPlaceholders(content, "/Users/tap-user/Software/homebrew-tap", "/Users/tap-user")

	assertOnlyAnchorPlaceholders(t, suggestions, "/main")
}

func TestDiscoverPlaceholders_DropsTildePathFragments(t *testing.T) {
	content := []byte(`anchor /Users/tap-user/Software/homebrew-tap config at ~/.ccs/config.json done`)

	suggestions := export.DiscoverPlaceholders(content, "/Users/tap-user/Software/homebrew-tap", "/Users/tap-user")

	assertOnlyAnchorPlaceholders(t, suggestions, "/.ccs/config.json")
}

func TestDiscoverPlaceholders_DropsUniversalAbsolutePaths(t *testing.T) {
	content := []byte(`anchor /Users/tap-user/Software/homebrew-tap and brew audit 2>/dev/null done`)

	suggestions := export.DiscoverPlaceholders(content, "/Users/tap-user/Software/homebrew-tap", "/Users/tap-user")

	assertOnlyAnchorPlaceholders(t, suggestions, "/dev/null")
}

func TestDiscoverPlaceholders_DropsPseudoXMLTags(t *testing.T) {
	content := []byte(`anchor /Users/tap-user/Software/homebrew-tap content </EXTREMELY_IMPORTANT> done`)

	suggestions := export.DiscoverPlaceholders(content, "/Users/tap-user/Software/homebrew-tap", "/Users/tap-user")

	assertOnlyAnchorPlaceholders(t, suggestions, "/EXTREMELY_IMPORTANT")
}

func TestDiscoverPlaceholders_DropsBareFilenamesInProse(t *testing.T) {
	content := []byte(`anchor /Users/tap-user/Software/homebrew-tap referenced copilot-tools.md and that/copilot-tools.md slug done`)

	suggestions := export.DiscoverPlaceholders(content, "/Users/tap-user/Software/homebrew-tap", "/Users/tap-user")

	assertOnlyAnchorPlaceholders(t, suggestions, "/copilot-tools.md")
}

func TestDiscoverPlaceholders_DropsProjectSubFragmentFromURLs(t *testing.T) {
	content := []byte(`anchor /Users/tap-user/Software/homebrew-tap and remote git@github.com:it-bens/homebrew-tap.git fetched`)

	suggestions := export.DiscoverPlaceholders(content, "/Users/tap-user/Software/homebrew-tap", "/Users/tap-user")

	assertOnlyAnchorPlaceholders(t, suggestions, "/homebrew-tap.git")
}

func TestDiscoverPlaceholders_OnHomebrewTapNoiseCorpus(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("testdata", "discover", "noise-session.jsonl"))
	require.NoError(t, err)

	suggestions := export.DiscoverPlaceholders(
		content,
		"/Users/tap-user/Software/homebrew-tap",
		"/Users/tap-user",
	)

	require.Len(t, suggestions, 2)
	assert.Equal(t, "{{PROJECT_PATH}}", suggestions[0].Key)
	assert.Equal(t, "/Users/tap-user/Software/homebrew-tap", suggestions[0].Original)
	assert.Equal(t, "{{HOME}}", suggestions[1].Key)
	assert.Equal(t, "/Users/tap-user", suggestions[1].Original)
}

// assertOnlyAnchorPlaceholders fails the test if suggestions contains anything
// beyond the two anchor keys, or if any noiseFragment appears as an Original.
func assertOnlyAnchorPlaceholders(
	t *testing.T,
	suggestions []export.PlaceholderSuggestion,
	noiseFragment string,
) {
	t.Helper()
	for _, suggestion := range suggestions {
		assert.Contains(t, []string{"{{PROJECT_PATH}}", "{{HOME}}"}, suggestion.Key,
			"unexpected placeholder key %q with original %q", suggestion.Key, suggestion.Original)
		assert.NotContains(t, suggestion.Original, noiseFragment,
			"noise fragment %q leaked into placeholder original %q", noiseFragment, suggestion.Original)
	}
}
