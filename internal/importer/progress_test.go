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
// extract phase exactly once per staged non-metadata entry: the cumulative Done
// values must run 1, 2, …, Total. A single Advance jumping straight to Total (a
// batch counter rather than per-entry) lands on the right final number but
// breaks the strict per-entry sequence, so this catches it.
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

	var extractDoneValues []int64
	for _, advance := range progresstest.OfType[progress.PhaseAdvance](events) {
		if len(advance.Path) == 1 && advance.Path[0] == "extract" {
			extractDoneValues = append(extractDoneValues, advance.Done)
		}
	}
	require.NotEmpty(t, extractDoneValues, "extract must advance at least once")

	var want []int64
	for entry := int64(1); entry <= extractStart.Total; entry++ {
		want = append(want, entry)
	}
	assert.Equal(t, want, extractDoneValues,
		"extract Done must increase by one per staged entry up to Total")
}
