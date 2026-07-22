package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/it-bens/cc-port/internal/rewrite"
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

func TestFinalize_SynthesizesWitnessAttributingImportedSessionToDestination(t *testing.T) {
	projectPath := "/Users/test/Projects/imported"
	importedSessionID := "11111111-1111-4111-8111-111111111111"
	workspace := newMergeTestWorkspace(t)

	projectDir := workspace.home.ProjectDir(projectPath)
	require.NoError(t, os.MkdirAll(projectDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, importedSessionID+".jsonl"),
		[]byte(`{"type":"user","cwd":"`+projectPath+`","sessionId":"`+importedSessionID+`"}`+"\n"),
		0o600,
	))

	_, err := workspace.Finalize(context.Background(), projectPath, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(workspace.home.SessionsDir(), importedSessionID+".json"))
	require.NoError(t, err, "import must write a session witness for the imported session")
	var witness sessionWitness
	require.NoError(t, json.Unmarshal(data, &witness))
	assert.Equal(t, importedSessionID, witness.SessionID)
	assert.Equal(t, projectPath, witness.Cwd, "witness must attribute the session to the destination project")
	assert.LessOrEqual(t, witness.Pid, 0, "a synthesized witness must never read as a live writer")
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

func TestMergeProjectConfigBytes_DropsIncomingApprovalGatesOnFreshDestination(t *testing.T) {
	projectPath := "/fresh/project"
	block := []byte(`{"hasTrustDialogAccepted":true,` +
		`"hasClaudeMdExternalIncludesApproved":false,` +
		`"hasClaudeMdExternalIncludesWarningShown":true,` +
		`"allowedTools":["Bash(ls)"],"setting":"enabled"}`)

	merged, err := mergeProjectConfigBytes(nil, "/fake/config", projectPath, block)

	require.NoError(t, err)
	path := "projects." + rewrite.EscapeSJSONKey(projectPath)
	for _, key := range destinationOwnedProjectKeys {
		assert.False(t, gjson.GetBytes(merged, path+"."+key).Exists())
	}
	assert.Equal(t, []interface{}{"Bash(ls)"}, gjson.GetBytes(merged, path+".allowedTools").Value())
	assert.Equal(t, "enabled", gjson.GetBytes(merged, path+".setting").String())
}

func TestMergeProjectConfigBytes_PreservesDestinationApprovalGateValues(t *testing.T) {
	projectPath := "/conflict/project"
	existing := []byte(`{"projects":{"/conflict/project":{"hasTrustDialogAccepted":true,"hasClaudeMdExternalIncludesApproved":false}}}`)
	block := []byte(`{"hasTrustDialogAccepted":false,"hasClaudeMdExternalIncludesApproved":true,"setting":"ported"}`)

	merged, err := mergeProjectConfigBytes(existing, "/fake/config", projectPath, block)

	require.NoError(t, err)
	path := "projects." + rewrite.EscapeSJSONKey(projectPath)
	assert.True(t, gjson.GetBytes(merged, path+".hasTrustDialogAccepted").Bool())
	assert.False(t, gjson.GetBytes(merged, path+".hasClaudeMdExternalIncludesApproved").Bool())
	assert.Equal(t, "ported", gjson.GetBytes(merged, path+".setting").String())
}

func TestMergeProjectConfigBytes_DropsIncomingApprovalGatesWhenDestinationProjectAbsent(t *testing.T) {
	projectPath := "/absent/project"
	existing := []byte(`{"projects":{"/different/project":{"setting":"existing"}}}`)
	block := []byte(`{"hasTrustDialogAccepted":true,` +
		`"hasClaudeMdExternalIncludesApproved":false,` +
		`"hasClaudeMdExternalIncludesWarningShown":true,"setting":"ported"}`)

	merged, err := mergeProjectConfigBytes(existing, "/fake/config", projectPath, block)

	require.NoError(t, err)
	path := "projects." + rewrite.EscapeSJSONKey(projectPath)
	for _, key := range destinationOwnedProjectKeys {
		assert.False(t, gjson.GetBytes(merged, path+"."+key).Exists())
	}
	assert.Equal(t, "ported", gjson.GetBytes(merged, path+".setting").String())
}

func TestMergeProjectConfigBytes_IsFixedPointAgainstUnchangedDestination(t *testing.T) {
	projectPath := "/fixed/project"
	existing := []byte(`{"projects":{"/fixed/project":{"hasTrustDialogAccepted":true,` +
		`"hasClaudeMdExternalIncludesApproved":false,` +
		`"hasClaudeMdExternalIncludesWarningShown":true}}}`)
	block := []byte(`{"hasTrustDialogAccepted":false,` +
		`"hasClaudeMdExternalIncludesApproved":true,` +
		`"hasClaudeMdExternalIncludesWarningShown":false,"setting":"ported"}`)

	result1, err := mergeProjectConfigBytes(existing, "/fake/config", projectPath, block)
	require.NoError(t, err)
	result2, err := mergeProjectConfigBytes(result1, "/fake/config", projectPath, block)

	require.NoError(t, err)
	assert.Equal(t, result1, result2)
}
