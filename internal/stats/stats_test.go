package stats_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/stats"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

const fixtureProject = "/Users/test/Projects/myproject"

func referenceCount(t *testing.T, footprint *stats.Footprint, surface string) int {
	t.Helper()
	for _, reference := range footprint.References {
		if reference.Surface == surface {
			return reference.Count
		}
	}
	t.Fatalf("reference surface %q not present", surface)
	return 0
}

func diskUsage(t *testing.T, usages []stats.DiskUsage, category string) stats.DiskUsage {
	t.Helper()
	for _, usage := range usages {
		if usage.Category == category {
			return usage
		}
	}
	t.Fatalf("disk category %q not present", category)
	return stats.DiskUsage{}
}

// TestComputeFootprint_ReferencesExcludePrefixSiblings is the boundary guard:
// the fixture deliberately seeds /Users/test/Projects/myproject-extras
// references in history.jsonl, .claude.json, and settings.json. A naive
// strings.Count would fold those into myproject's tally; the boundary-aware
// count must not. Each expected value is one less than the naive count.
func TestComputeFootprint_ReferencesExcludePrefixSiblings(t *testing.T) {
	home := testutil.SetupFixture(t)

	footprint, err := stats.ComputeFootprint(t.Context(), home, fixtureProject)
	require.NoError(t, err)

	assert.Equal(t, 5, referenceCount(t, footprint, "history"),
		"history line for myproject-extras must not count toward myproject")
	assert.Equal(t, 4, referenceCount(t, footprint, "config"),
		"the myproject-extras project key must not count toward myproject")
	assert.Equal(t, 4, referenceCount(t, footprint, "settings"),
		"the myproject-extras marketplace path must not count toward myproject")
}

// TestComputeFootprint_StructuredCountsDifferFromReferences pins the design
// point that the structured history-entry count (lines whose project field
// equals the path) and the reference count (path occurrences across all
// well-formed lines, including free text) are deliberately different numbers.
func TestComputeFootprint_StructuredCountsDifferFromReferences(t *testing.T) {
	home := testutil.SetupFixture(t)

	footprint, err := stats.ComputeFootprint(t.Context(), home, fixtureProject)
	require.NoError(t, err)

	assert.Equal(t, 4, footprint.HistoryEntryCount, "four history lines name myproject in their project field")
	assert.Equal(t, 5, referenceCount(t, footprint, "history"),
		"one of those lines also embeds the path in its display text, so references exceed entries")
	assert.Equal(t, 1, footprint.SessionFileCount)
}

func TestComputeFootprint_DiskFootprintByCategory(t *testing.T) {
	home := testutil.SetupFixture(t)

	footprint, err := stats.ComputeFootprint(t.Context(), home, fixtureProject)
	require.NoError(t, err)

	// file-history spans two snapshot dirs (session …001 with 5 files, …003
	// with 2); the snapshots are sized but never read.
	assert.Equal(t, 7, diskUsage(t, footprint.Disk, "file-history").Files)
	// sessions = 2 top-level transcripts + 3 session-subdir bodies + 1 sessions/*.json.
	assert.Equal(t, 6, diskUsage(t, footprint.Disk, "sessions").Files)

	// memory holds the four project memory files. The session-keyed categories
	// attribute files by the project's session UUIDs; only session …001 carries
	// todos/usage-data/plugins-data/tasks entries. The tasks .lock and
	// .highwatermark sidecars are filtered by AllFlatFiles, leaving only 1.json.
	assert.Equal(t, 4, diskUsage(t, footprint.Disk, "memory").Files)
	assert.Equal(t, 1, diskUsage(t, footprint.Disk, "todos").Files)
	assert.Equal(t, 2, diskUsage(t, footprint.Disk, "usage-data").Files)
	assert.Equal(t, 1, diskUsage(t, footprint.Disk, "plugins-data").Files)
	assert.Equal(t, 1, diskUsage(t, footprint.Disk, "tasks").Files)

	// history and config are shared globals: no per-project disk footprint.
	assert.Equal(t, stats.DiskUsage{Category: "history"}, diskUsage(t, footprint.Disk, "history"))
	assert.Equal(t, stats.DiskUsage{Category: "config"}, diskUsage(t, footprint.Disk, "config"))
}

// TestComputeFootprint_DiskCategoriesFollowManifestOrder guards the claim that
// the disk breakdown derives its key ordering from manifest.AllCategories
// rather than a hard-coded slice.
func TestComputeFootprint_DiskCategoriesFollowManifestOrder(t *testing.T) {
	home := testutil.SetupFixture(t)

	footprint, err := stats.ComputeFootprint(t.Context(), home, fixtureProject)
	require.NoError(t, err)

	expected := make([]string, len(manifest.AllCategories))
	for index, spec := range manifest.AllCategories {
		expected[index] = spec.Name
	}
	actual := make([]string, len(footprint.Disk))
	for index, usage := range footprint.Disk {
		actual[index] = usage.Category
	}
	assert.Equal(t, expected, actual)
}

// TestComputeFootprint_ReferenceSurfacesDeriveFromRegistries guards that every
// user-wide and session-keyed registry entry is a reference surface and that
// file-history (opaque snapshots) is not.
func TestComputeFootprint_ReferenceSurfacesDeriveFromRegistries(t *testing.T) {
	home := testutil.SetupFixture(t)

	footprint, err := stats.ComputeFootprint(t.Context(), home, fixtureProject)
	require.NoError(t, err)

	present := make(map[string]bool, len(footprint.References))
	for _, reference := range footprint.References {
		present[reference.Surface] = true
	}
	for target := range claude.UserWideRewriteTargets() {
		assert.True(t, present[target.Name], "user-wide target %q must be a reference surface", target.Name)
	}
	for group := range claude.SessionKeyedGroups() {
		assert.True(t, present[group.Name], "session-keyed group %q must be a reference surface", group.Name)
	}
	assert.False(t, present["file-history"], "file-history snapshots are opaque and carry no references")
}

// TestComputeFootprint_CountsEncodedStorageDir proves the encoded-storage-dir
// second pass fires: the memory file holds only the absolute encoded directory
// form, which the slashed real path never matches, so a real-path-only count
// would be zero.
func TestComputeFootprint_CountsEncodedStorageDir(t *testing.T) {
	home := newEmptyHome(t)
	projectPath := "/Users/test/Projects/demo"
	encodedDir := home.ProjectDir(projectPath)

	memoryDir := filepath.Join(encodedDir, "memory")
	require.NoError(t, os.MkdirAll(memoryDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(memoryDir, "note.md"),
		[]byte("storage lives at "+encodedDir+"\n"),
		0o600,
	))

	footprint, err := stats.ComputeFootprint(t.Context(), home, projectPath)
	require.NoError(t, err)

	assert.Equal(t, 1, referenceCount(t, footprint, "memory"),
		"the encoded-dir reference must be counted even though the slashed path is absent")
}

func TestComputeFootprint_NotFoundPropagates(t *testing.T) {
	home := testutil.SetupFixture(t)

	footprint, err := stats.ComputeFootprint(t.Context(), home, "/no/such/project")
	require.Error(t, err)
	assert.Nil(t, footprint, "a missing project must error, not fabricate a zero footprint")
}

// TestComputeAllFootprints_RanksByBytesDescending checks the all-projects mode:
// every fixture project resolves a witness label (including the lossy
// "my project" spelling), and the ranking is byte-descending.
func TestComputeAllFootprints_RanksByBytesDescending(t *testing.T) {
	home := testutil.SetupFixture(t)

	footprints, err := stats.ComputeAllFootprints(t.Context(), home)
	require.NoError(t, err)
	require.Len(t, footprints, 4)

	for index := 1; index < len(footprints); index++ {
		assert.GreaterOrEqual(t, footprints[index-1].Bytes, footprints[index].Bytes,
			"footprints must be ranked by bytes descending")
	}

	assert.Equal(t, fixtureProject, footprints[0].Label, "the richest project ranks first")

	labels := make(map[string]bool, len(footprints))
	for _, footprint := range footprints {
		assert.True(t, footprint.Resolved, "every fixture project has a session witness")
		labels[footprint.Label] = true
	}
	assert.True(t, labels["/Users/test/Projects/my project"],
		"the witness label must use the space spelling, not the encoded dir name")
}

func TestComputeAllFootprints_EmptyHomeYieldsNone(t *testing.T) {
	home := newEmptyHome(t)

	footprints, err := stats.ComputeAllFootprints(t.Context(), home)
	require.NoError(t, err)
	assert.Empty(t, footprints)
}

// newEmptyHome returns a Home rooted at a fresh temp directory with no staged
// data, for cases that need a controlled tree.
func newEmptyHome(t *testing.T) *claude.Home {
	t.Helper()
	dir := t.TempDir()
	return &claude.Home{Dir: filepath.Join(dir, "dotclaude"), ConfigFile: filepath.Join(dir, "dotclaude.json")}
}

// writeStatsFile writes content at path under an isolated home, creating parent
// directories. The reference-count assertions depend on the exact bytes, so the
// helper takes the content verbatim.
func writeStatsFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// stageWitnessedProject stages an encoded project dir with one neutral transcript
// and a matching sessions/*.json witness so LocateProject resolves identity
// without a stderr warning. It returns the encoded project dir.
func stageWitnessedProject(t *testing.T, home *claude.Home, projectPath string) string {
	t.Helper()
	const sessionUUID = "aaaaaaaa-0000-0000-0000-000000000001"
	encodedDir := home.ProjectDir(projectPath)
	writeStatsFile(t, filepath.Join(encodedDir, sessionUUID+".jsonl"), "{}\n")
	writeStatsFile(t, filepath.Join(home.SessionsDir(), sessionUUID+".json"),
		fmt.Sprintf(`{"sessionId":%q,"cwd":%q,"pid":2000000001}`, sessionUUID, projectPath))
	return encodedDir
}

// TestComputeFootprint_NonUUIDBodySubdirCountedAndSized is the fix-A acceptance
// guard. A non-UUID, non-memory/sessions subdir under the project dir holds a
// body file with a project-path reference. The transcript reference count must
// include its occurrence and the sessions disk usage must size it. The old walk
// only descended UUID-named subdirs, so it dropped this file entirely: it would
// report 0 transcript references and 2 (not 3) sessions files.
func TestComputeFootprint_NonUUIDBodySubdirCountedAndSized(t *testing.T) {
	home := newEmptyHome(t)
	const projectPath = "/Users/test/Projects/demo"
	encodedDir := stageWitnessedProject(t, home, projectPath)

	writeStatsFile(t, filepath.Join(encodedDir, "workspace", "agent.jsonl"),
		fmt.Sprintf(`{"cwd":%q}`+"\n", projectPath))

	footprint, err := stats.ComputeFootprint(t.Context(), home, projectPath)
	require.NoError(t, err)

	assert.Equal(t, 1, referenceCount(t, footprint, "transcripts"),
		"the non-UUID subdir body file's path reference must be counted")
	// neutral transcript + workspace/agent.jsonl + the sessions/*.json witness.
	assert.Equal(t, 3, diskUsage(t, footprint.Disk, "sessions").Files,
		"the non-UUID subdir body file's bytes must be sized into the sessions category")
}

// TestComputeFootprint_CountsJSONEscapedReferences pins the JSON-escape count
// variant on every surface that uses it (history, config, sessions): each holds
// the project path only in its `\/`-escaped form, so swapping any of these
// surfaces to the raw variant drops the occurrence and fails here. No fixture
// surface carries the escaped form, so the primitive-layer coverage alone does
// not catch a per-surface mis-assignment.
func TestComputeFootprint_CountsJSONEscapedReferences(t *testing.T) {
	home := newEmptyHome(t)
	const (
		projectPath = "/Users/test/Projects/demo"
		sessionUUID = "aaaaaaaa-0000-0000-0000-000000000001"
	)
	encodedDir := home.ProjectDir(projectPath)
	escaped := strings.ReplaceAll(projectPath, "/", `\/`)

	writeStatsFile(t, filepath.Join(encodedDir, sessionUUID+".jsonl"), "{}\n")
	// The witness carries the raw path in cwd plus an escaped occurrence, so the
	// escape variant counts 2 where the raw variant would count only the cwd.
	writeStatsFile(t, filepath.Join(home.SessionsDir(), sessionUUID+".json"),
		fmt.Sprintf(`{"sessionId":%q,"cwd":%q,"pid":2000000001,"note":"see %s here"}`, sessionUUID, projectPath, escaped))
	writeStatsFile(t, home.HistoryFile(),
		fmt.Sprintf(`{"project":"/Users/test/Projects/other","display":"at %s end"}`+"\n", escaped))
	writeStatsFile(t, home.ConfigFile,
		fmt.Sprintf(`{"projects":{},"note":"ref %s"}`, escaped))

	footprint, err := stats.ComputeFootprint(t.Context(), home, projectPath)
	require.NoError(t, err)

	assert.Equal(t, 1, referenceCount(t, footprint, "history"),
		"the escaped history occurrence must count; the raw variant would miss it")
	assert.Equal(t, 1, referenceCount(t, footprint, "config"),
		"the escaped config occurrence must count; the raw variant would miss it")
	assert.Equal(t, 2, referenceCount(t, footprint, "sessions"),
		"the raw cwd plus the escaped note must both count; the raw variant would yield 1")
}

// TestComputeFootprint_SkipsMalformedHistoryLine pins the malformed-line skip in
// the history reference count. The second line embeds the project path but is
// not valid JSON; an apply preserves such lines verbatim, so they carry no
// rewritable reference. Counting it would report 2 instead of 1.
func TestComputeFootprint_SkipsMalformedHistoryLine(t *testing.T) {
	home := newEmptyHome(t)
	const projectPath = "/Users/test/Projects/demo"
	stageWitnessedProject(t, home, projectPath)

	writeStatsFile(t, home.HistoryFile(),
		`{"project":"/Users/test/Projects/demo"}`+"\n"+
			`{"project":"/Users/test/Projects/demo"`+"\n")

	footprint, err := stats.ComputeFootprint(t.Context(), home, projectPath)
	require.NoError(t, err)

	assert.Equal(t, 1, referenceCount(t, footprint, "history"),
		"the malformed line embeds the path but must be skipped; counting it yields 2")
}

// TestComputeFootprint_CountsEncodedStorageDirInTranscript exercises the
// encoded-dir second pass on a real transcript body (the existing encoded-dir
// test uses a memory file). The transcript holds only the absolute storage-dir
// form, which the slashed real path never matches, so a real-path-only count is
// zero.
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

	footprint, err := stats.ComputeFootprint(t.Context(), home, projectPath)
	require.NoError(t, err)

	assert.Equal(t, 1, referenceCount(t, footprint, "transcripts"),
		"the encoded storage-dir form in a real transcript must be counted by the second pass")
}

// TestComputeFootprint_DiskMatchesAllProjectsEntry cross-checks that, for a
// witness-resolved project, the per-category disk usage a single-project
// footprint reports equals the same project's entry in the all-projects
// ranking. The equality is conditional: the all-projects walk skips
// sessions/*.json for a witness-less project (it has no real path to attribute
// them by), while single-project mode always includes them. The fixture
// project resolves a witness, asserted below.
func TestComputeFootprint_DiskMatchesAllProjectsEntry(t *testing.T) {
	home := testutil.SetupFixture(t)

	footprint, err := stats.ComputeFootprint(t.Context(), home, fixtureProject)
	require.NoError(t, err)

	footprints, err := stats.ComputeAllFootprints(t.Context(), home)
	require.NoError(t, err)

	var matched *stats.ProjectFootprint
	for index := range footprints {
		if footprints[index].Label == fixtureProject {
			matched = &footprints[index]
		}
	}
	require.NotNil(t, matched, "the fixture project must appear in the all-projects ranking")
	require.True(t, matched.Resolved,
		"the cross-mode disk equality holds only for a witness-resolved project")

	assert.Equal(t, footprint.Disk, matched.Disk,
		"per-category disk usage matches between modes for a witness-resolved project")
}
