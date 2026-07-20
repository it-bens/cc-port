package claude_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool/claude"
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

	enumerations, err := claude.EnumerateProjects(context.Background(), claudeHome)
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

	enumerations, err := claude.EnumerateProjects(context.Background(), claudeHome)
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

	enumerations, err := claude.EnumerateProjects(context.Background(), claudeHome)
	require.NoError(t, err)

	require.Len(t, enumerations, 1)
	assert.Empty(t, enumerations[0].ResolvedPath, "no session witness means no resolved label")
	assert.NotEmpty(t, enumerations[0].Locations.SessionTranscripts,
		"disk metrics are still computed without a witness")
}

func TestEnumerateProjects_EmptyProjectsDirYieldsNoProjects(t *testing.T) {
	claudeHome := newEmptyHome(t)

	enumerations, err := claude.EnumerateProjects(context.Background(), claudeHome)
	require.NoError(t, err)
	assert.Empty(t, enumerations, "an absent projects directory is not an error")
}

// TestEnumerateProjects_CancelledContextOnMissingProjectsDirReturnsError pins
// that a canceled context is never masked as "nothing to enumerate": an
// absent projects directory and a canceled context produce the same shape
// of result (empty slice, nil-looking) unless the entry-time and
// missing-directory checks distinguish them. Without those checks a caller
// could not tell a canceled run apart from a genuinely empty home.
func TestEnumerateProjects_CancelledContextOnMissingProjectsDirReturnsError(t *testing.T) {
	claudeHome := newEmptyHome(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	enumerations, err := claude.EnumerateProjects(ctx, claudeHome)

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, enumerations, "a canceled context must not return a plausible-looking empty result")
}

func TestEnumerateProjects_SkipsNonDirectoryEntries(t *testing.T) {
	claudeHome := newEmptyHome(t)
	require.NoError(t, os.MkdirAll(claudeHome.ProjectsDir(), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeHome.ProjectsDir(), "stray.txt"), []byte("x"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(claudeHome.ProjectsDir(), "-tmp-real"), 0o750))

	enumerations, err := claude.EnumerateProjects(context.Background(), claudeHome)
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

	enumerations, err := claude.EnumerateProjects(context.Background(), claudeHome)
	require.NoError(t, err, "a witness disagreement is not an error in all-projects mode")

	labels := resolvedPathByEncodedName(enumerations)
	assert.Equal(t, "/Users/test/Projects/one", labels["-Users-test-Projects-collide"],
		"the first witness by sorted session-file name must win the tie-break")
}

// countdownContext cancels the wrapped context the moment its Err method
// has been consulted callsUntilCancel+1 times, rather than being canceled
// up front. This lets a test force cancellation to land at a specific point
// inside a walk deterministically (no wall-clock race, no dependence on
// scheduling) instead of only proving the trivial pre-canceled case.
type countdownContext struct {
	context.Context
	cancel           context.CancelFunc
	callsUntilCancel int
}

func (c *countdownContext) Err() error {
	if c.callsUntilCancel <= 0 {
		c.cancel()
	} else {
		c.callsUntilCancel--
	}
	return c.Context.Err()
}

// TestEnumerateProjects_CancelsMidEnumeration pins that cancellation
// observed partway through resolveWitnessPath's session-witness walk stops
// the whole enumeration before it reaches a second, later project (finding
// CL1): walkSessionWitnesses — the dominant cost the finding named, since
// it scans the entire shared sessions/ directory once per project rather
// than a bounded per-project set — must honor ctx just as the outer
// per-project loop does.
//
// None of the ten decoy witness files match project "a"'s own UUID, so
// resolveWitnessPath finds no match and collectSessionFiles (gated on a
// resolved path) never runs; project "a" otherwise declares no other
// session-UUID-keyed data, so none of its other collectors ever consult
// ctx either. That leaves walkSessionWitnesses's own per-entry check as
// the only one the budget below can land on. A context pre-canceled from
// the start would only prove the outer loop's check fires before any work
// starts; countdownContext instead lets several witnesses be inspected
// first, so cancellation genuinely lands mid-walk. This isolation was
// verified by temporarily disabling walkSessionWitnesses's check alone
// (every other new check in this package stayed active): the test then
// completes successfully with no error, since nothing else in the run
// consumes enough of the budget to trip cancellation on its own.
func TestEnumerateProjects_CancelsMidEnumeration(t *testing.T) {
	claudeHome := newEmptyHome(t)
	require.NoError(t, os.MkdirAll(claudeHome.ProjectsDir(), 0o750))
	require.NoError(t, os.MkdirAll(claudeHome.SessionsDir(), 0o750))

	const projectAUUID = "aaaaaaaa-0000-4000-8000-000000000000"
	projectADir := filepath.Join(claudeHome.ProjectsDir(), "-tmp-project-a")
	require.NoError(t, os.MkdirAll(projectADir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(projectADir, projectAUUID+".jsonl"), []byte("{}\n"), 0o600))

	// None of these match projectAUUID, so walkSessionWitnesses scans all
	// ten without ever finding a witness for project "a".
	const decoyWitnessCount = 10
	for index := range decoyWitnessCount {
		decoyUUID := fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
		decoyWitness := fmt.Sprintf(`{"sessionId":%q,"cwd":"/Users/test/Projects/decoy"}`, decoyUUID)
		require.NoError(t, os.WriteFile(filepath.Join(claudeHome.SessionsDir(), decoyUUID+".json"), []byte(decoyWitness), 0o600))
	}

	// A second project, sorted after "a", that must never be reached if
	// cancellation genuinely lands while still walking the decoy witnesses.
	require.NoError(t, os.MkdirAll(filepath.Join(claudeHome.ProjectsDir(), "-tmp-project-b"), 0o750))

	baseCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// Allows 6 non-canceled Err() calls: the outer per-project loop's check
	// for project "a", collectProjectDirEntries's single check for its one
	// transcript file, then the first four decoy witnesses. The 7th call
	// (the fifth decoy) triggers cancel(), well before the tenth decoy or
	// project "a"'s other collectors (which never run: no witness match
	// means no resolved path, and its lone UUID never gets an independent
	// exhaustive scan the way the shared sessions/ directory does).
	ctx := &countdownContext{Context: baseCtx, cancel: cancel, callsUntilCancel: 6}

	enumerations, err := claude.EnumerateProjects(ctx, claudeHome)

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, enumerations, "a mid-walk cancellation must not return a partial enumeration")
}

// newEmptyHome returns a Home rooted at a fresh temp directory with no staged
// data, for enumeration edge cases that need a controlled tree.
func newEmptyHome(t *testing.T) *claude.Home {
	t.Helper()
	dir := t.TempDir()
	return &claude.Home{Dir: filepath.Join(dir, "dotclaude"), ConfigFile: filepath.Join(dir, "dotclaude.json")}
}
