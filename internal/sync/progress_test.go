package sync

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/progress/progresstest"
)

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

// phasePaths returns every PhaseStart path as a slash-joined string, in
// emission order, so a test can assert a nested phase opened under its parent.
func phasePaths(events []progress.Event) []string {
	var paths []string
	for _, start := range progresstest.OfType[progress.PhaseStart](events) {
		paths = append(paths, filepath.ToSlash(filepath.Join(start.Path...)))
	}
	return paths
}

// TestExecutePush_ExportSubPhasesNestAndUploadAdvances replicates applyPush's
// wiring inside the sync package: it opens the upload phase on the same root
// reporter ExecutePush uses, wraps the in-memory archive output with a
// CountingWriter, and runs ExecutePush. The assertions pin both halves of the
// push progress contract: export's own locate/archive sub-phases nest under the
// top-level "export" phase, and bytes written through the wrapped writer drive a
// PhaseAdvance on the sibling "upload" phase.
func TestExecutePush_ExportSubPhasesNestAndUploadAdvances(t *testing.T) {
	recorder := progresstest.NewRecorder()
	reporter := recorder.Reporter(progress.LevelInfo)

	home, projectPath := buildTestHomeAndProject(t)

	opts := PushOptions{
		ClaudeHome:  home,
		ProjectPath: projectPath,
		Name:        "k",
		Categories:  allCategoriesSet(),
		Reporter:    reporter,
	}
	plan, err := PlanPush(context.Background(), opts, nil)
	require.NoError(t, err)

	// Mirror applyPush: the upload phase is owned by the cmd layer and wraps the
	// outermost writer; ExecutePush opens the sibling export phase internally.
	var buf bytes.Buffer
	writer, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&encrypt.WriterStage{Pass: ""},
		&bufferSink{buf: &buf},
	})
	require.NoError(t, err)

	uploadPhase := reporter.Phase("upload", plan.PriorSize, progress.UnitBytes)
	countingWriter := progress.CountingWriter(writer, uploadPhase)

	require.NoError(t, ExecutePush(context.Background(), opts, plan, countingWriter))
	uploadPhase.End("")
	require.NoError(t, writer.Close())

	events := recorder.Events()

	assert.Contains(t, topLevelPhaseNames(events), "export",
		"ExecutePush opens a top-level export phase on its reporter")
	assert.Contains(t, phasePaths(events), "export/locate",
		"export's locate sub-phase nests under the export phase")
	assert.Contains(t, phasePaths(events), "export/archive",
		"export's archive sub-phase nests under the export phase")

	var uploadAdvances int
	var lastDone int64 = -1
	for _, advance := range progresstest.OfType[progress.PhaseAdvance](events) {
		if len(advance.Path) != 1 || advance.Path[0] != "upload" {
			continue
		}
		assert.GreaterOrEqual(t, advance.Done, lastDone, "upload Done must not decrease")
		lastDone = advance.Done
		uploadAdvances++
	}
	assert.Positive(t, uploadAdvances,
		"bytes written through the counting writer must advance the upload phase")
	assert.Positive(t, lastDone, "upload phase must record a non-zero byte count")
}

// TestExecutePull_ImportSubPhasesNest pushes a fixture archive, then pulls it
// with a recorder on PullOptions.Reporter and asserts importer's four phases
// (preflight, manifest, extract, promote) open nested under the top-level
// "import" phase ExecutePull brackets.
func TestExecutePull_ImportSubPhasesNest(t *testing.T) {
	r := newFileRemote(t)
	homeA, projectPathA := buildTestHomeAndProject(t)

	planA, err := PlanPush(context.Background(), PushOptions{
		ClaudeHome: homeA, ProjectPath: projectPathA, Name: "k",
		Categories: allCategoriesSet(),
	}, openPriorForTest(t, r, "k", ""))
	require.NoError(t, err)
	writerA := openWriterForTest(t, r, "k", "")
	require.NoError(t, ExecutePush(context.Background(), PushOptions{
		ClaudeHome: homeA, ProjectPath: projectPathA, Name: "k",
		Categories: allCategoriesSet(),
	}, planA, writerA))
	require.NoError(t, writerA.Close())

	homeB := buildTestHomeBlank(t)
	targetPath := filepath.Join(t.TempDir(), "pulled-project")

	recorder := progresstest.NewRecorder()
	source := openSourceForTest(t, r, "k", "")
	pullOpts := PullOptions{
		ClaudeHome: homeB, Name: "k", TargetPath: targetPath,
		Reporter: recorder.Reporter(progress.LevelInfo),
	}
	planB, err := PlanPull(context.Background(), pullOpts, source)
	require.NoError(t, err)
	require.Empty(t, planB.UnresolvedPlaceholders)

	_, err = ExecutePull(context.Background(), pullOpts, planB, source)
	require.NoError(t, err)

	events := recorder.Events()

	assert.Contains(t, topLevelPhaseNames(events), "import",
		"ExecutePull opens a top-level import phase on its reporter")
	paths := phasePaths(events)
	for _, sub := range []string{"preflight", "manifest", "extract", "promote"} {
		assert.Contains(t, paths, "import/"+sub,
			"importer's "+sub+" phase nests under the import phase")
	}
}
