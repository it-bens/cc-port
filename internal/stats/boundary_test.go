package stats_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/stats"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// newEmptyHome returns a Home rooted at a fresh temp directory with no
// staged data, for cases that need a controlled tree.
func newEmptyHome(t *testing.T) *claude.Home {
	t.Helper()
	dir := t.TempDir()
	return &claude.Home{Dir: filepath.Join(dir, "dotclaude"), ConfigFile: filepath.Join(dir, "dotclaude.json")}
}

func emptyHomeTargets(t *testing.T, home *claude.Home) []tool.Target {
	t.Helper()
	return []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
}

// writeStatsFile writes content at path, creating parent directories. The
// reference-count assertions depend on the exact bytes, so the helper takes
// the content verbatim.
func writeStatsFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// stageWitnessedProject stages an encoded project dir with one neutral
// transcript and a matching sessions/*.json witness so LocateProject
// resolves identity without a stderr warning. It returns the encoded
// project dir.
func stageWitnessedProject(t *testing.T, home *claude.Home, projectPath string) string {
	t.Helper()
	const sessionUUID = "aaaaaaaa-0000-0000-0000-000000000001"
	encodedDir := home.ProjectDir(projectPath)
	writeStatsFile(t, filepath.Join(encodedDir, sessionUUID+".jsonl"), "{}\n")
	writeStatsFile(t, filepath.Join(home.SessionsDir(), sessionUUID+".json"),
		fmt.Sprintf(`{"sessionId":%q,"cwd":%q,"pid":2000000001}`, sessionUUID, projectPath))
	return encodedDir
}

// TestComputeFootprint_NonUUIDBodySubdirCountedAndSized is the fix-A
// acceptance guard. A non-UUID, non-memory/sessions subdir under the
// project dir holds a body file with a project-path reference. The
// transcript reference count must include its occurrence and the sessions
// disk usage must size it. A walk that only descends UUID-named subdirs
// would drop this file entirely: it would report 0 transcript references
// and 2 (not 3) sessions files.
func TestComputeFootprint_NonUUIDBodySubdirCountedAndSized(t *testing.T) {
	home := newEmptyHome(t)
	const projectPath = "/Users/test/Projects/demo"
	encodedDir := stageWitnessedProject(t, home, projectPath)

	writeStatsFile(t, filepath.Join(encodedDir, "workspace", "agent.jsonl"),
		fmt.Sprintf(`{"cwd":%q}`+"\n", projectPath))

	footprint, err := stats.ComputeFootprint(t.Context(), emptyHomeTargets(t, home), projectPath)
	require.NoError(t, err)
	claudeFootprint := footprint.ByTool[0]

	assert.Equal(t, 1, referenceCount(t, &claudeFootprint, "transcripts"),
		"the non-UUID subdir body file's path reference must be counted")
	// neutral transcript + workspace/agent.jsonl + the sessions/*.json witness.
	assert.Equal(t, 3, diskUsage(t, claudeFootprint.Disk, "sessions").Files,
		"the non-UUID subdir body file's bytes must be sized into the sessions category")
}

// TestComputeFootprint_CountsJSONEscapedReferences pins the JSON-escape
// count variant on every surface that uses it (history, config, sessions):
// each holds the project path only in its `\/`-escaped form, so swapping
// any of these surfaces to the raw variant drops the occurrence and fails
// here.
func TestComputeFootprint_CountsJSONEscapedReferences(t *testing.T) {
	home := newEmptyHome(t)
	const (
		projectPath = "/Users/test/Projects/demo"
		sessionUUID = "aaaaaaaa-0000-0000-0000-000000000001"
	)
	encodedDir := home.ProjectDir(projectPath)
	escaped := strings.ReplaceAll(projectPath, "/", `\/`)

	writeStatsFile(t, filepath.Join(encodedDir, sessionUUID+".jsonl"), "{}\n")
	// The witness carries the raw path in cwd plus an escaped occurrence, so
	// the escape variant counts 2 where the raw variant would count only cwd.
	writeStatsFile(t, filepath.Join(home.SessionsDir(), sessionUUID+".json"),
		fmt.Sprintf(`{"sessionId":%q,"cwd":%q,"pid":2000000001,"note":"see %s here"}`, sessionUUID, projectPath, escaped))
	writeStatsFile(t, home.HistoryFile(),
		fmt.Sprintf(`{"project":"/Users/test/Projects/other","display":"at %s end"}`+"\n", escaped))
	writeStatsFile(t, home.ConfigFile,
		fmt.Sprintf(`{"projects":{},"note":"ref %s"}`, escaped))

	footprint, err := stats.ComputeFootprint(t.Context(), emptyHomeTargets(t, home), projectPath)
	require.NoError(t, err)
	claudeFootprint := footprint.ByTool[0]

	assert.Equal(t, 1, referenceCount(t, &claudeFootprint, "history"),
		"the escaped history occurrence must count; the raw variant would miss it")
	assert.Equal(t, 1, referenceCount(t, &claudeFootprint, "config"),
		"the escaped config occurrence must count; the raw variant would miss it")
	assert.Equal(t, 2, referenceCount(t, &claudeFootprint, "sessions"),
		"the raw cwd plus the escaped note must both count; the raw variant would yield 1")
}

// TestComputeFootprint_SkipsMalformedHistoryLine pins the malformed-line
// skip in the history reference count. The second line embeds the project
// path but is not valid JSON; an apply preserves such lines verbatim, so
// they carry no rewritable reference. Counting it would report 2 instead
// of 1.
func TestComputeFootprint_SkipsMalformedHistoryLine(t *testing.T) {
	home := newEmptyHome(t)
	const projectPath = "/Users/test/Projects/demo"
	stageWitnessedProject(t, home, projectPath)

	writeStatsFile(t, home.HistoryFile(),
		`{"project":"/Users/test/Projects/demo"}`+"\n"+
			`{"project":"/Users/test/Projects/demo"`+"\n")

	footprint, err := stats.ComputeFootprint(t.Context(), emptyHomeTargets(t, home), projectPath)
	require.NoError(t, err)
	claudeFootprint := footprint.ByTool[0]

	assert.Equal(t, 1, referenceCount(t, &claudeFootprint, "history"),
		"the malformed line embeds the path but must be skipped; counting it yields 2")
}

// TestComputeFootprint_CountsEncodedStorageDir proves the encoded-storage-dir
// second pass fires: the memory file holds only the absolute encoded
// directory form, which the slashed real path never matches, so a
// real-path-only count would be zero.
func TestComputeFootprint_CountsEncodedStorageDir(t *testing.T) {
	home := newEmptyHome(t)
	const projectPath = "/Users/test/Projects/demo"
	encodedDir := home.ProjectDir(projectPath)

	memoryDir := filepath.Join(encodedDir, "memory")
	require.NoError(t, os.MkdirAll(memoryDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(memoryDir, "note.md"),
		[]byte("storage lives at "+encodedDir+"\n"),
		0o600,
	))

	footprint, err := stats.ComputeFootprint(t.Context(), emptyHomeTargets(t, home), projectPath)
	require.NoError(t, err)
	claudeFootprint := footprint.ByTool[0]

	assert.Equal(t, 1, referenceCount(t, &claudeFootprint, "memory"),
		"the encoded-dir reference must be counted even though the slashed path is absent")
}

// TestComputeFootprint_CountsEncodedStorageDirInTranscript exercises the
// encoded-dir second pass on a real transcript body (the sibling test above
// uses a memory file). The transcript holds only the absolute storage-dir
// form, which the slashed real path never matches, so a real-path-only
// count is zero.
func TestComputeFootprint_CountsEncodedStorageDirInTranscript(t *testing.T) {
	home := newEmptyHome(t)
	const (
		projectPath = "/Users/test/Projects/demo"
		sessionUUID = "aaaaaaaa-0000-0000-0000-000000000001"
	)
	encodedDir := home.ProjectDir(projectPath)

	writeStatsFile(t, filepath.Join(encodedDir, sessionUUID+".jsonl"),
		fmt.Sprintf(`{"storage":%q}`+"\n", encodedDir))
	writeStatsFile(t, filepath.Join(home.SessionsDir(), sessionUUID+".json"),
		fmt.Sprintf(`{"sessionId":%q,"cwd":%q,"pid":2000000001}`, sessionUUID, projectPath))

	footprint, err := stats.ComputeFootprint(t.Context(), emptyHomeTargets(t, home), projectPath)
	require.NoError(t, err)
	claudeFootprint := footprint.ByTool[0]

	assert.Equal(t, 1, referenceCount(t, &claudeFootprint, "transcripts"),
		"the encoded storage-dir form in a real transcript must be counted by the second pass")
}
