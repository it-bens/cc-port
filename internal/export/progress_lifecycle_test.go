package export_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/progress/progresstest"
)

// topLevelPhaseNames returns the names of every top-level phase opened on
// the recorder, in emission order. A top-level phase is a PhaseStart whose
// Path has exactly one segment.
func topLevelPhaseNames(events []progress.Event) []string {
	var names []string
	for _, start := range progresstest.OfType[progress.PhaseStart](events) {
		if len(start.Path) == 1 {
			names = append(names, start.Path[0])
		}
	}
	return names
}

// archiveSubPhaseNames returns the set of sub-phase names opened directly
// under the archive phase.
func archiveSubPhaseNames(events []progress.Event) map[string]struct{} {
	names := make(map[string]struct{})
	for _, start := range progresstest.OfType[progress.PhaseStart](events) {
		if len(start.Path) == 2 && start.Path[0] == "archive" {
			names[start.Path[1]] = struct{}{}
		}
	}
	return names
}

// TestRun_EmitsArchiveTopLevelPhase pins the generic export progress
// lifecycle: "archive" is the sole top-level phase Run opens. Per-tool
// export no longer reports its own phase (tool.Exporter.Export takes no
// Reporter), so there is no separate "locate" phase at this layer.
func TestRun_EmitsArchiveTopLevelPhase(t *testing.T) {
	targets, projectPath := fixtureTargets(t)

	recorder := progresstest.NewRecorder()
	var buf bytes.Buffer
	_, err := export.Run(context.Background(), targets, &export.Options{
		ProjectPath: projectPath,
		Output:      &buf,
		Selected:    map[string]map[string]bool{"claude": allSelected(targets[0].Tool)},
		Reporter:    recorder.Reporter(progress.LevelInfo),
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"archive"}, topLevelPhaseNames(recorder.Events()))
}

// TestRun_ArchiveOpensOneSubPhasePerTarget is the export progress
// drift-guard for the multi-tool architecture: the archive phase's
// granularity is one tool, not one manifest category (that reporting moved
// into each adapter, which today reports nothing at all). Every target Run
// exports opens exactly one archive sub-phase, named after the tool.
func TestRun_ArchiveOpensOneSubPhasePerTarget(t *testing.T) {
	targets, projectPath := fixtureTargets(t)

	recorder := progresstest.NewRecorder()
	var buf bytes.Buffer
	_, err := export.Run(context.Background(), targets, &export.Options{
		ProjectPath: projectPath,
		Output:      &buf,
		Selected:    map[string]map[string]bool{"claude": allSelected(targets[0].Tool)},
		Reporter:    recorder.Reporter(progress.LevelInfo),
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]struct{}{"claude": {}}, archiveSubPhaseNames(recorder.Events()),
		"archive opens exactly one sub-phase, named after the exported tool")
}

// TestRun_ArchiveSubPhaseOpensEvenWithEmptySelection pins that a target's
// sub-phase opens unconditionally: unlike the old per-category phases, an
// empty category selection for a tool does not suppress that tool's
// sub-phase.
func TestRun_ArchiveSubPhaseOpensEvenWithEmptySelection(t *testing.T) {
	targets, projectPath := fixtureTargets(t)

	recorder := progresstest.NewRecorder()
	var buf bytes.Buffer
	_, err := export.Run(context.Background(), targets, &export.Options{
		ProjectPath: projectPath,
		Output:      &buf,
		Selected:    map[string]map[string]bool{},
		Reporter:    recorder.Reporter(progress.LevelInfo),
	})
	require.NoError(t, err)

	assert.Contains(t, archiveSubPhaseNames(recorder.Events()), "claude",
		"every target still opens a sub-phase even when it has nothing selected")
}
