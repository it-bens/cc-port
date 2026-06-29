package claude_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/testutil"
)

// resolvedPathByEncodedName indexes an enumeration slice by encoded directory
// name so a test can assert the label resolved for a specific project.
func resolvedPathByEncodedName(enumerations []claude.ProjectEnumeration) map[string]string {
	byName := make(map[string]string, len(enumerations))
	for _, enumeration := range enumerations {
		byName[enumeration.EncodedName] = enumeration.ResolvedPath
	}
	return byName
}

func TestEnumerateProjects_ResolvesWitnessLabels(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	enumerations, err := claude.EnumerateProjects(claudeHome)
	require.NoError(t, err)

	labels := resolvedPathByEncodedName(enumerations)
	// The witness resolves the lossy encoding: "my project" (space) and
	// "my-project" (hyphen) both encode to -Users-test-Projects-my-project,
	// and only a session cwd recovers which real path it was.
	assert.Equal(t, "/Users/test/Projects/my project", labels["-Users-test-Projects-my-project"],
		"witness cwd must win over a naive decode of the encoded name")
	assert.Equal(t, "/Users/test/Projects/myproject", labels["-Users-test-Projects-myproject"])
	assert.Equal(t, "/Users/test/Projects/myproject-extras", labels["-Users-test-Projects-myproject-extras"])
	assert.Equal(t, "/Users/test/Projects/my project.v2", labels["-Users-test-Projects-my-project-v2"])
}

func TestEnumerateProjects_PopulatesDiskLocations(t *testing.T) {
	claudeHome := testutil.SetupFixture(t)

	enumerations, err := claude.EnumerateProjects(claudeHome)
	require.NoError(t, err)

	var myproject *claude.ProjectEnumeration
	for index := range enumerations {
		if enumerations[index].EncodedName == "-Users-test-Projects-myproject" {
			myproject = &enumerations[index]
		}
	}
	require.NotNil(t, myproject, "fixture's primary project must be enumerated")

	assert.NotEmpty(t, myproject.Locations.SessionTranscripts, "transcripts size the sessions category")
	assert.NotEmpty(t, myproject.Locations.MemoryFiles, "memory files size the memory category")
	assert.NotEmpty(t, myproject.Locations.FileHistoryDirs, "file-history dirs size the file-history category")
	assert.NotEmpty(t, myproject.Locations.SessionFiles,
		"sessions/*.json are attributed once the witness resolves the real path")
}

func TestEnumerateProjects_WitnessLessLabelFallsBackToEmpty(t *testing.T) {
	claudeHome := newEmptyHome(t)
	orphanDir := filepath.Join(claudeHome.ProjectsDir(), "-tmp-orphan")
	require.NoError(t, os.MkdirAll(orphanDir, 0o750))
	transcript := filepath.Join(orphanDir, "aaaaaaaa-0000-0000-0000-000000000001.jsonl")
	require.NoError(t, os.WriteFile(transcript, []byte("{}\n"), 0o600))

	enumerations, err := claude.EnumerateProjects(claudeHome)
	require.NoError(t, err)

	require.Len(t, enumerations, 1)
	assert.Empty(t, enumerations[0].ResolvedPath, "no session witness means no resolved label")
	assert.NotEmpty(t, enumerations[0].Locations.SessionTranscripts,
		"disk metrics are still computed without a witness")
}

func TestEnumerateProjects_EmptyProjectsDirYieldsNoProjects(t *testing.T) {
	claudeHome := newEmptyHome(t)

	enumerations, err := claude.EnumerateProjects(claudeHome)
	require.NoError(t, err)
	assert.Empty(t, enumerations, "an absent projects directory is not an error")
}

func TestEnumerateProjects_SkipsNonDirectoryEntries(t *testing.T) {
	claudeHome := newEmptyHome(t)
	require.NoError(t, os.MkdirAll(claudeHome.ProjectsDir(), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeHome.ProjectsDir(), "stray.txt"), []byte("x"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(claudeHome.ProjectsDir(), "-tmp-real"), 0o750))

	enumerations, err := claude.EnumerateProjects(claudeHome)
	require.NoError(t, err)

	require.Len(t, enumerations, 1, "a stray file under projects/ must not be enumerated")
	assert.Equal(t, "-tmp-real", enumerations[0].EncodedName)
}

// TestEnumerateProjects_MultiWitnessDisagreementPicksFirstByName pins
// resolveWitnessPath's tie-break: when two session witnesses attribute one
// encoded directory to different real paths (a lossy-encoding collision), the
// first by sorted session-file name wins, deterministically. Every shared
// fixture encoded dir resolves exactly one witness, so this case needs its own
// tree.
func TestEnumerateProjects_MultiWitnessDisagreementPicksFirstByName(t *testing.T) {
	claudeHome := newEmptyHome(t)
	const (
		uuidA = "aaaaaaaa-0000-0000-0000-000000000001"
		uuidB = "bbbbbbbb-0000-0000-0000-000000000002"
	)
	encodedDir := filepath.Join(claudeHome.ProjectsDir(), "-Users-test-Projects-collide")
	require.NoError(t, os.MkdirAll(encodedDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(encodedDir, uuidA+".jsonl"), []byte("{}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(encodedDir, uuidB+".jsonl"), []byte("{}\n"), 0o600))

	require.NoError(t, os.MkdirAll(claudeHome.SessionsDir(), 0o750))
	// Session-file names sort uuidA before uuidB, so the witness for uuidA wins.
	require.NoError(t, os.WriteFile(filepath.Join(claudeHome.SessionsDir(), uuidA+".json"),
		[]byte(fmt.Sprintf(`{"sessionId":%q,"cwd":"/Users/test/Projects/one"}`, uuidA)), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(claudeHome.SessionsDir(), uuidB+".json"),
		[]byte(fmt.Sprintf(`{"sessionId":%q,"cwd":"/Users/test/Projects/two"}`, uuidB)), 0o600))

	enumerations, err := claude.EnumerateProjects(claudeHome)
	require.NoError(t, err, "a witness disagreement is not an error in all-projects mode")

	labels := resolvedPathByEncodedName(enumerations)
	assert.Equal(t, "/Users/test/Projects/one", labels["-Users-test-Projects-collide"],
		"the first witness by sorted session-file name must win the tie-break")
}

// newEmptyHome returns a Home rooted at a fresh temp directory with no staged
// data, for enumeration edge cases that need a controlled tree.
func newEmptyHome(t *testing.T) *claude.Home {
	t.Helper()
	dir := t.TempDir()
	return &claude.Home{Dir: filepath.Join(dir, "dotclaude"), ConfigFile: filepath.Join(dir, "dotclaude.json")}
}
