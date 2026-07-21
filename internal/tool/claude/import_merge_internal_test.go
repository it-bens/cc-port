package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMergeTestWorkspace returns a Workspace rooted at a fresh temp home, with
// no history.jsonl or config file yet on disk.
func newMergeTestWorkspace(t *testing.T) *Workspace {
	t.Helper()
	dir := t.TempDir()
	home := &Home{Dir: filepath.Join(dir, "dotclaude"), ConfigFile: filepath.Join(dir, "dotclaude.json")}
	return NewWorkspace(home)
}

func TestFinalize_ReportsRulesFileReferences(t *testing.T) {
	projectPath := "/Users/test/Projects/myproject"
	workspace := newMergeTestWorkspace(t)
	require.NoError(t, os.MkdirAll(workspace.home.RulesDir(), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace.home.RulesDir(), "import-rule.md"),
		[]byte("Applies to /Users/test/Projects/myproject only.\n"),
		0o600,
	))

	warnings, err := workspace.Finalize(context.Background(), projectPath, nil)

	require.NoError(t, err)
	assert.Contains(t, warnings, "rules file import-rule.md (line 1) references this project")
}

func TestFinalizeHistory_EmptyExistingPrependsNothing(t *testing.T) {
	workspace := newMergeTestWorkspace(t)
	workspace.historyAppends = [][]byte{[]byte("line1\n"), []byte("line2\n")}

	require.NoError(t, workspace.finalizeHistory())

	got, err := os.ReadFile(workspace.home.HistoryFile())
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\n", string(got))
}

func TestFinalizeHistory_ExistingEndsWithNewlineKeepsSingleSeparator(t *testing.T) {
	workspace := newMergeTestWorkspace(t)
	require.NoError(t, os.MkdirAll(workspace.home.Dir, 0o750))
	require.NoError(t, os.WriteFile(workspace.home.HistoryFile(), []byte("line1\n"), 0o600))
	workspace.historyAppends = [][]byte{[]byte("line2\n")}

	require.NoError(t, workspace.finalizeHistory())

	got, err := os.ReadFile(workspace.home.HistoryFile())
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\n", string(got))
}

func TestFinalizeHistory_ExistingMissingTrailingNewlineInsertsOne(t *testing.T) {
	workspace := newMergeTestWorkspace(t)
	require.NoError(t, os.MkdirAll(workspace.home.Dir, 0o750))
	require.NoError(t, os.WriteFile(workspace.home.HistoryFile(), []byte("line1"), 0o600))
	workspace.historyAppends = [][]byte{[]byte("line2\n")}

	require.NoError(t, workspace.finalizeHistory())

	got, err := os.ReadFile(workspace.home.HistoryFile())
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\n", string(got))
}

func TestFinalizeHistory_MultipleAppendsConcatInOrder(t *testing.T) {
	workspace := newMergeTestWorkspace(t)
	require.NoError(t, os.MkdirAll(workspace.home.Dir, 0o750))
	require.NoError(t, os.WriteFile(workspace.home.HistoryFile(), []byte("a\n"), 0o600))
	workspace.historyAppends = [][]byte{[]byte("b\n"), []byte("c\n"), []byte("d\n")}

	require.NoError(t, workspace.finalizeHistory())

	got, err := os.ReadFile(workspace.home.HistoryFile())
	require.NoError(t, err)
	assert.Equal(t, "a\nb\nc\nd\n", string(got))
}

func TestMergeProjectConfigBytes_EmptyExistingStartsFromObject(t *testing.T) {
	block := []byte(`{"setting":1}`)
	got, err := mergeProjectConfigBytes(nil, "/fake/config", "/proj", block)
	require.NoError(t, err)
	assert.JSONEq(t, `{"projects":{"/proj":{"setting":1}}}`, string(got))
}

func TestMergeProjectConfigBytes_PreservesSiblingKeys(t *testing.T) {
	existing := []byte(`{"theme":"dark","projects":{"/other":{"x":1}}}`)
	block := []byte(`{"setting":2}`)
	got, err := mergeProjectConfigBytes(existing, "/fake/config", "/proj", block)
	require.NoError(t, err)
	assert.Contains(t, string(got), `"theme":"dark"`, "sibling top-level key must survive")
	assert.Contains(t, string(got), `"/other":{"x":1}`, "sibling project must survive")
	assert.Contains(t, string(got), `"/proj":{"setting":2}`, "new project must be present")
}

func TestMergeProjectConfigBytes_RejectsInvalidJSON(t *testing.T) {
	existing := []byte(`{not valid json`)
	_, err := mergeProjectConfigBytes(existing, "/fake/config", "/proj", []byte(`{}`))
	var configErr *InvalidConfigJSONError
	require.ErrorAs(t, err, &configErr)
	assert.Equal(t, "/fake/config", configErr.Path)
}
