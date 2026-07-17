package importer_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/progress/progresstest"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// topLevelPhaseNames returns the names of every top-level phase opened on the
// recorder, in emission order. A top-level phase is a PhaseStart whose Path
// has exactly one segment.
func topLevelPhaseNames(events []progress.Event) []string {
	var names []string
	for _, start := range progresstest.OfType[progress.PhaseStart](events) {
		if len(start.Path) == 1 {
			names = append(names, start.Path[0])
		}
	}
	return names
}

// recordSuccessfulImport runs a successful import of a freshly built fixture
// archive into an empty destination home, with recorder wired as the
// reporter, and returns the recorded events.
func recordSuccessfulImport(t *testing.T) []progress.Event {
	t.Helper()
	body, projectPath := buildArchive(t)
	home := blankHome(t)
	toolSet := tool.NewSet(claude.New())
	targets := []tool.Target{{Tool: toolSet.All()[0], Workspace: claude.NewWorkspace(home)}}
	recorder := progresstest.NewRecorder()

	_, err := importer.Run(context.Background(), toolSet, targets, &importer.Options{
		Source:     bytes.NewReader(body),
		Size:       int64(len(body)),
		TargetPath: projectPath,
		Reporter:   recorder.Reporter(progress.LevelInfo),
	})
	require.NoError(t, err)
	return recorder.Events()
}

func TestImport_EmitsTopLevelPhasesInOrder(t *testing.T) {
	events := recordSuccessfulImport(t)

	assert.Equal(t,
		[]string{"preflight", "extract", "promote", "finalize"},
		topLevelPhaseNames(events),
		"import emits preflight, extract, promote, then finalize",
	)
}

// TestImport_ExtractCountsEveryStagedEntry asserts the importer advances the
// extract phase exactly once per staged entry: the cumulative Done values
// must run 1, 2, …, Total. A single Advance jumping straight to Total (a
// batch counter rather than per-entry) lands on the right final number but
// breaks the strict per-entry sequence, so this catches it.
func TestImport_ExtractCountsEveryStagedEntry(t *testing.T) {
	events := recordSuccessfulImport(t)

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
