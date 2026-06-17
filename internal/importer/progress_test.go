package importer_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/progress/progresstest"
	"github.com/it-bens/cc-port/internal/testutil"
)

// recordSuccessfulImport runs a successful import of the fixture archive into
// an empty destination home with the supplied recorder wired as the reporter.
func recordSuccessfulImport(t *testing.T, recorder *progresstest.Recorder) {
	t.Helper()

	sourceClaudeHome := testutil.SetupFixture(t)
	archivePath := filepath.Join(t.TempDir(), "export.zip")
	buildTestArchive(t, sourceClaudeHome, archivePath)

	destClaudeHome := buildEmptyDestClaudeHome(t)
	destHomeDir := filepath.Join(t.TempDir(), "home")

	source, size := openArchive(t, archivePath)
	_, err := importer.Run(t.Context(), destClaudeHome, &importer.Options{
		Source:     source,
		Size:       size,
		TargetPath: fixtureDestProjectPath,
		Resolutions: map[string]string{
			"{{PROJECT_PATH}}": fixtureDestProjectPath,
			"{{HOME}}":         destHomeDir,
		},
		Reporter: recorder.Reporter(progress.LevelInfo),
	})
	require.NoError(t, err)
}

// topLevelPhaseNames returns the names of every top-level phase opened on the
// recorder, in emission order. A top-level phase is a PhaseStart whose Path has
// exactly one segment.
func topLevelPhaseNames(events []progress.Event) []string {
	var names []string
	for _, start := range progresstest.OfType[progress.PhaseStart](events) {
		if len(start.Path) == 1 {
			names = append(names, start.Path[0])
		}
	}
	return names
}

func TestImport_EmitsTopLevelPhasesInOrder(t *testing.T) {
	recorder := progresstest.NewRecorder()
	recordSuccessfulImport(t, recorder)

	assert.Equal(t,
		[]string{"preflight", "manifest", "extract", "promote"},
		topLevelPhaseNames(recorder.Events()),
		"import emits preflight, manifest, extract, then promote",
	)
}

// TestImport_ExtractCountsEveryStagedEntry asserts the importer advances the
// extract phase once per staged non-metadata entry: the final cumulative Done
// must equal the phase's declared Total, so a single Advance covering all
// entries at once (a per-entry-counting bug) would fail.
func TestImport_ExtractCountsEveryStagedEntry(t *testing.T) {
	recorder := progresstest.NewRecorder()
	recordSuccessfulImport(t, recorder)

	events := recorder.Events()

	var extractStart progress.PhaseStart
	foundStart := false
	for _, start := range progresstest.OfType[progress.PhaseStart](events) {
		if len(start.Path) == 1 && start.Path[0] == "extract" {
			extractStart = start
			foundStart = true
		}
	}
	require.True(t, foundStart, "extract phase must open")

	var extractAdvances []progress.PhaseAdvance
	for _, advance := range progresstest.OfType[progress.PhaseAdvance](events) {
		if len(advance.Path) == 1 && advance.Path[0] == "extract" {
			extractAdvances = append(extractAdvances, advance)
		}
	}
	require.NotEmpty(t, extractAdvances, "extract must advance at least once")

	lastAdvance := extractAdvances[len(extractAdvances)-1]
	assert.Equal(t, extractStart.Total, lastAdvance.Done,
		"extract Done must equal the number of staged entries")
}
