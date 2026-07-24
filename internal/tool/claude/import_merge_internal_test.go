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
	workspace.stagedSessionUUIDs[importedSessionID] = struct{}{}

	_, err := workspace.Finalize(context.Background(), projectPath, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(workspace.home.SessionsDir(), importedSessionID+".json"))
	require.NoError(t, err, "import must write a session witness for the imported session")
	var witness sessionWitness
	require.NoError(t, json.Unmarshal(data, &witness))
	assert.Equal(t, importedSessionID, witness.SessionID)
	assert.Equal(t, projectPath, witness.Cwd, "witness must attribute the session to the destination project")
	assert.Equal(t, 0, witness.Pid, "a synthesized witness records the inert pid 0 documented as its contract")
}

func TestStagedSessionUUID(t *testing.T) {
	sessionID := "11111111-1111-4111-8111-111111111111"
	cases := []struct {
		name     string
		relative string
		want     string
		wantOK   bool
	}{
		{"transcript file", sessionID + ".jsonl", sessionID, true},
		{"session subdirectory entry", sessionID + "/tool-uses.json", sessionID, true},
		{"directory named like a transcript", sessionID + ".jsonl/tool-uses.json", "", false},
		{"non-uuid leading segment", "notes.txt", "", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := stagedSessionUUID(testCase.relative)
			assert.Equal(t, testCase.wantOK, ok)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestFinalize_WithoutStagedSessionsWritesNoWitness(t *testing.T) {
	workspace := newMergeTestWorkspace(t)

	_, err := workspace.Finalize(context.Background(), "/Users/test/Projects/imported", nil)
	require.NoError(t, err)

	assert.NoDirExists(t, workspace.home.SessionsDir(),
		"an import that stages no session must not create the sessions directory")
}

func TestFinalize_WitnessesOnlyStagedSessionsNotCoLocatedForeignSessions(t *testing.T) {
	projectPath := "/Users/test/Projects/imported"
	stagedSessionID := "11111111-1111-4111-8111-111111111111"
	foreignSessionID := "22222222-2222-4222-8222-222222222222"
	workspace := newMergeTestWorkspace(t)
	workspace.stagedSessionUUIDs[stagedSessionID] = struct{}{}

	// A prior import of a different real path that encodes to this same
	// directory (a lossy-encoding collision) left its own transcript behind.
	projectDir := workspace.home.ProjectDir(projectPath)
	require.NoError(t, os.MkdirAll(projectDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, foreignSessionID+".jsonl"), []byte("{}\n"), 0o600))

	_, err := workspace.Finalize(context.Background(), projectPath, nil)
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(workspace.home.SessionsDir(), stagedSessionID+".json"))
	assert.NoFileExists(t, filepath.Join(workspace.home.SessionsDir(), foreignSessionID+".json"),
		"a co-located session this import did not stage must not be witnessed")
}

func TestFinalize_LeavesAPreexistingWitnessUntouched(t *testing.T) {
	projectPath := "/Users/test/Projects/imported"
	stagedSessionID := "11111111-1111-4111-8111-111111111111"
	workspace := newMergeTestWorkspace(t)
	workspace.stagedSessionUUIDs[stagedSessionID] = struct{}{}

	// A live Claude session names its witness by PID; synthesis names by
	// session UUID, so the two can never share a filename.
	sessionsDir := workspace.home.SessionsDir()
	require.NoError(t, os.MkdirAll(sessionsDir, 0o750))
	liveWitnessPath := filepath.Join(sessionsDir, "4242.json")
	liveWitness := []byte(`{"cwd":"/Users/test/Projects/other","pid":4242}`)
	require.NoError(t, os.WriteFile(liveWitnessPath, liveWitness, 0o600))

	_, err := workspace.Finalize(context.Background(), projectPath, nil)
	require.NoError(t, err)

	got, err := os.ReadFile(liveWitnessPath) //nolint:gosec // G304: path from t.TempDir
	require.NoError(t, err)
	assert.Equal(t, liveWitness, got, "synthesis must not overwrite a live Claude-written witness")
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

func TestMergeProjectConfigBytes_RejectsMalformedIncomingBlock(t *testing.T) {
	merged, err := mergeProjectConfigBytes(nil, "/fake/config", "/proj", []byte(`{"setting":`))
	require.Error(t, err, "a malformed archive block must never be spliced into the destination")
	assert.Nil(t, merged)
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
	assert.False(t, gjson.GetBytes(merged, path+".allowedTools").Exists(),
		"incoming allowedTools is destination-owned on the config splice and ports only via config-grants")
	assert.Equal(t, "enabled", gjson.GetBytes(merged, path+".setting").String())
}

func TestMergeProjectConfigBytes_PreservesDestinationApprovalGateValues(t *testing.T) {
	projectPath := "/conflict/project"
	existing := []byte(`{"projects":{"/conflict/project":{"hasTrustDialogAccepted":true,` +
		`"hasClaudeMdExternalIncludesApproved":false,"allowedTools":["Bash(go:*)"]}}}`)
	block := []byte(`{"hasTrustDialogAccepted":false,"hasClaudeMdExternalIncludesApproved":true,` +
		`"allowedTools":["Bash(rm:*)"],"setting":"ported"}`)

	merged, err := mergeProjectConfigBytes(existing, "/fake/config", projectPath, block)

	require.NoError(t, err)
	path := "projects." + rewrite.EscapeSJSONKey(projectPath)
	assert.True(t, gjson.GetBytes(merged, path+".hasTrustDialogAccepted").Bool())
	assert.False(t, gjson.GetBytes(merged, path+".hasClaudeMdExternalIncludesApproved").Bool())
	assert.Equal(t, []interface{}{"Bash(go:*)"}, gjson.GetBytes(merged, path+".allowedTools").Value(),
		"the destination's allowedTools must survive the config splice")
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

func TestMergeProjectConfigBytes_TreatsMetacharacterPathAsLiteralKey(t *testing.T) {
	projectPath := "/Users/test/Projects/proj?v2"
	siblingPath := "/Users/test/Projects/projxv2"
	existing := []byte(`{"projects":{"/Users/test/Projects/projxv2":{"setting":"sibling"}}}`)
	block := []byte(`{"setting":"ported"}`)

	merged, err := mergeProjectConfigBytes(existing, "/fake/config", projectPath, block)

	require.NoError(t, err)
	var top struct {
		Projects map[string]json.RawMessage `json:"projects"`
	}
	require.NoError(t, json.Unmarshal(merged, &top))
	require.Contains(t, top.Projects, projectPath,
		"a project path carrying a gjson/sjson metacharacter must be created as a literal key")
	assert.JSONEq(t, `{"setting":"ported"}`, string(top.Projects[projectPath]))
	assert.JSONEq(t, `{"setting":"sibling"}`, string(top.Projects[siblingPath]),
		"a sibling project the unescaped wildcard would match must stay untouched")
}

func TestFinalizeConfigGrants_SplicesIncomingAllowedToolsOverDestination(t *testing.T) {
	projectPath := "/Users/test/Projects/granted"
	workspace := newMergeTestWorkspace(t)
	existing := []byte(`{"projects":{"/Users/test/Projects/granted":{"allowedTools":["Bash(go:*)"],"setting":"kept"}}}`)
	require.NoError(t, os.WriteFile(workspace.home.ConfigFile, existing, 0o600))
	workspace.configGrantsBlock = []byte(`{"allowedTools":["Bash(rm:*)"]}`)

	require.NoError(t, workspace.finalizeConfigGrants(projectPath))

	merged, err := os.ReadFile(workspace.home.ConfigFile)
	require.NoError(t, err)
	path := "projects." + rewrite.EscapeSJSONKey(projectPath)
	assert.Equal(t, []interface{}{"Bash(rm:*)"}, gjson.GetBytes(merged, path+".allowedTools").Value(),
		"a selected config-grants category must port the incoming grants over the destination's")
	assert.Equal(t, "kept", gjson.GetBytes(merged, path+".setting").String())

	require.NoError(t, workspace.finalizeConfigGrants(projectPath))
	rerun, err := os.ReadFile(workspace.home.ConfigFile)
	require.NoError(t, err)
	assert.Equal(t, merged, rerun, "re-running the grants splice against an unchanged destination must be byte-identical")
}

func TestFinalizeConfigGrants_RejectsMalformedGrantsBlock(t *testing.T) {
	workspace := newMergeTestWorkspace(t)
	workspace.configGrantsBlock = []byte(`{"allowedTools":[`)

	err := workspace.finalizeConfigGrants("/Users/test/Projects/granted")

	require.Error(t, err, "a malformed grants block must never be spliced into the destination")
	assert.NoFileExists(t, workspace.home.ConfigFile)
}

func TestFinalizeConfigGrants_RejectsInvalidExistingConfig(t *testing.T) {
	workspace := newMergeTestWorkspace(t)
	require.NoError(t, os.WriteFile(workspace.home.ConfigFile, []byte(`{not valid json`), 0o600))
	workspace.configGrantsBlock = []byte(`{"allowedTools":["Bash(go:*)"]}`)

	err := workspace.finalizeConfigGrants("/Users/test/Projects/granted")

	var configErr *InvalidConfigJSONError
	require.ErrorAs(t, err, &configErr)
	assert.Equal(t, workspace.home.ConfigFile, configErr.Path)
}

func TestFinalizeConfigGrants_EmptyBlockLeavesDestinationUntouched(t *testing.T) {
	workspace := newMergeTestWorkspace(t)
	existing := []byte(`{"projects":{"/Users/test/Projects/granted":{"allowedTools":["Bash(go:*)"]}}}`)
	require.NoError(t, os.WriteFile(workspace.home.ConfigFile, existing, 0o600))
	workspace.configGrantsBlock = []byte(`{}`)

	require.NoError(t, workspace.finalizeConfigGrants("/Users/test/Projects/granted"))

	got, err := os.ReadFile(workspace.home.ConfigFile)
	require.NoError(t, err)
	assert.Equal(t, existing, got,
		"a grants block without allowedTools must leave the destination byte-identical")
}

func TestFinalizeConfigGrants_SplicesOntoMetacharacterPath(t *testing.T) {
	projectPath := "/Users/test/Projects/@scope/issue#42"
	workspace := newMergeTestWorkspace(t)
	workspace.configGrantsBlock = []byte(`{"allowedTools":["Bash(go:*)"]}`)

	require.NoError(t, workspace.finalizeConfigGrants(projectPath))

	merged, err := os.ReadFile(workspace.home.ConfigFile)
	require.NoError(t, err)
	var top struct {
		Projects map[string]json.RawMessage `json:"projects"`
	}
	require.NoError(t, json.Unmarshal(merged, &top))
	require.Contains(t, top.Projects, projectPath,
		"the grants splice must create the literal metacharacter key instead of silently dropping the write")
	assert.JSONEq(t, `{"allowedTools":["Bash(go:*)"]}`, string(top.Projects[projectPath]))
}

func TestFinalize_ConfigGrantsSpliceWinsOverDestinationOwnedHandling(t *testing.T) {
	projectPath := "/Users/test/Projects/granted"
	workspace := newMergeTestWorkspace(t)
	workspace.configBlock = []byte(`{"allowedTools":["Bash(incoming:*)"],"setting":"ported"}`)
	workspace.configGrantsBlock = []byte(`{"allowedTools":["Bash(granted:*)"]}`)

	_, err := workspace.Finalize(context.Background(), projectPath, nil)
	require.NoError(t, err)

	merged, err := os.ReadFile(workspace.home.ConfigFile)
	require.NoError(t, err)
	path := "projects." + rewrite.EscapeSJSONKey(projectPath)
	assert.Equal(t, []interface{}{"Bash(granted:*)"}, gjson.GetBytes(merged, path+".allowedTools").Value(),
		"the grants entry's value must land, not the config block's destination-owned copy")
	assert.Equal(t, "ported", gjson.GetBytes(merged, path+".setting").String())
}

func TestMergeProjectConfigBytes_IsFixedPointAgainstUnchangedDestination(t *testing.T) {
	projectPath := "/fixed/project"
	existing := []byte(`{"projects":{"/fixed/project":{"hasTrustDialogAccepted":true,` +
		`"hasClaudeMdExternalIncludesApproved":false,` +
		`"hasClaudeMdExternalIncludesWarningShown":true,"allowedTools":["Bash(go:*)"]}}}`)
	block := []byte(`{"hasTrustDialogAccepted":false,` +
		`"hasClaudeMdExternalIncludesApproved":true,` +
		`"hasClaudeMdExternalIncludesWarningShown":false,` +
		`"allowedTools":["Bash(rm:*)"],"setting":"ported"}`)

	result1, err := mergeProjectConfigBytes(existing, "/fake/config", projectPath, block)
	require.NoError(t, err)
	result2, err := mergeProjectConfigBytes(result1, "/fake/config", projectPath, block)

	require.NoError(t, err)
	assert.Equal(t, result1, result2)
}
