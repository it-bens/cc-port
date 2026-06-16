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

// TestImport_ExtractAdvancesMonotonically locks the extract counter to the real
// staging iteration: PhaseAdvance.Done is cumulative, so it must be
// non-decreasing, and at least one advance must fire because the fixture
// archive stages non-metadata entries.
func TestImport_ExtractAdvancesMonotonically(t *testing.T) {
	recorder := progresstest.NewRecorder()
	recordSuccessfulImport(t, recorder)

	previousDone := int64(-1)
	advances := 0
	for _, advance := range progresstest.OfType[progress.PhaseAdvance](recorder.Events()) {
		if len(advance.Path) != 1 || advance.Path[0] != "extract" {
			continue
		}
		assert.GreaterOrEqual(t, advance.Done, previousDone,
			"extract Done must not decrease")
		previousDone = advance.Done
		advances++
	}
	assert.Positive(t, advances,
		"extract must advance at least once per staged archive entry")
}
