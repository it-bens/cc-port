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

// TestExecutePush_ExportSubPhasesNest asserts ExecutePush opens a top-level
// "export" phase on its reporter and nests its own locate/archive sub-phases
// under it.
func TestExecutePush_ExportSubPhasesNest(t *testing.T) {
	recorder := progresstest.NewRecorder()
	reporter := recorder.Reporter(progress.LevelInfo)

	targets, projectPath := buildTestTargets(t)

	opts := PushOptions{
		Targets:     targets,
		ProjectPath: projectPath,
		Name:        "k",
		Selected:    allSelection(),
		Reporter:    reporter,
	}
	plan, err := PlanPush(context.Background(), opts, nil)
	require.NoError(t, err)

	var buf bytes.Buffer
	writer, err := pipeline.RunWriter(context.Background(), []pipeline.WriterStage{
		&encrypt.WriterStage{Pass: ""},
		&bufferSink{buf: &buf},
	})
	require.NoError(t, err)

	require.NoError(t, ExecutePush(context.Background(), opts, plan, writer))
	require.NoError(t, writer.Close())

	events := recorder.Events()

	assert.Contains(t, topLevelPhaseNames(events), "export",
		"ExecutePush opens a top-level export phase on its reporter")
	assert.Contains(t, phasePaths(events), "export/archive",
		"export's archive sub-phase nests under the export phase")
	assert.Contains(t, phasePaths(events), "export/archive/claude",
		"export's per-tool sub-phase nests under the archive phase")
}

// TestExecutePull_ImportSubPhasesNest pushes a fixture archive, then pulls it
// with a recorder on PullOptions.Reporter and asserts importer's four phases
// (preflight, manifest, extract, promote) open nested under the top-level
// "import" phase ExecutePull brackets.
func TestExecutePull_ImportSubPhasesNest(t *testing.T) {
	r := newFileRemote(t)
	targetsA, projectPathA := buildTestTargets(t)

	planA, err := PlanPush(context.Background(), PushOptions{
		Targets: targetsA, ProjectPath: projectPathA, Name: "k",
		Selected: allSelection(),
	}, openPriorForTest(t, r, "k", ""))
	require.NoError(t, err)
	writerA := openWriterForTest(t, r, "k", "")
	require.NoError(t, ExecutePush(context.Background(), PushOptions{
		Targets: targetsA, ProjectPath: projectPathA, Name: "k",
		Selected: allSelection(),
	}, planA, writerA))
	require.NoError(t, writerA.Close())

	homeB := buildTestHomeBlank(t)
	targetsB := targetsFor(homeB)
	targetPath := filepath.Join(t.TempDir(), "pulled-project")

	recorder := progresstest.NewRecorder()
	source := openSourceForTest(t, r, "k", "")
	pullOpts := PullOptions{
		AllTools: toolSetForTest(), Targets: targetsB, Name: "k", TargetPath: targetPath,
		Reporter: recorder.Reporter(progress.LevelInfo),
	}
	planB, err := PlanPull(context.Background(), pullOpts, source)
	require.NoError(t, err)
	require.Empty(t, planB.UnresolvedPlaceholders["claude"])

	_, err = ExecutePull(context.Background(), pullOpts, planB, source)
	require.NoError(t, err)

	events := recorder.Events()

	assert.Contains(t, topLevelPhaseNames(events), "import",
		"ExecutePull opens a top-level import phase on its reporter")
	paths := phasePaths(events)
	for _, sub := range []string{"preflight", "extract", "promote", "finalize"} {
		assert.Contains(t, paths, "import/"+sub,
			"importer's "+sub+" phase nests under the import phase")
	}
}
