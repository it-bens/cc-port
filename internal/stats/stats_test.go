package stats_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/stats"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

const fixtureProject = "/Users/test/Projects/myproject"

type cancelAfterFirstCheckContext struct {
	context.Context
	checks int
}

func (ctx *cancelAfterFirstCheckContext) Done() <-chan struct{} {
	return nil
}

func (ctx *cancelAfterFirstCheckContext) Err() error {
	ctx.checks++
	if ctx.checks == 1 {
		return nil
	}
	return context.Canceled
}

func fixtureTargets(t *testing.T) []tool.Target {
	t.Helper()
	home := testutil.SetupFixture(t)
	return []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
}

func referenceCount(t *testing.T, toolFootprint *stats.ToolFootprint, surface string) int {
	t.Helper()
	for _, reference := range toolFootprint.References {
		if reference.Name == surface {
			return reference.Count
		}
	}
	t.Fatalf("reference surface %q not present", surface)
	return 0
}

func diskUsage(t *testing.T, usages []tool.SizeCategory, category string) tool.SizeCategory {
	t.Helper()
	for _, usage := range usages {
		if usage.Name == category {
			return usage
		}
	}
	t.Fatalf("disk category %q not present", category)
	return tool.SizeCategory{}
}

func TestComputeFootprint_OneEntryPerTarget(t *testing.T) {
	targets := fixtureTargets(t)

	footprint, err := stats.ComputeFootprint(t.Context(), targets, fixtureProject)
	require.NoError(t, err)
	require.Len(t, footprint.ByTool, 1)
	assert.Equal(t, "claude", footprint.ByTool[0].Tool)
	assert.False(t, footprint.ByTool[0].Absent)
}

func TestAuditorReferenceSurfaces_CancelsBeforeSecondHistoryLine(t *testing.T) {
	targets := fixtureTargets(t)
	ctx := &cancelAfterFirstCheckContext{Context: context.Background()}

	_, err := targets[0].Workspace.ReferenceSurfaces(ctx, fixtureProject)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 2, ctx.checks, "the scan must stop before the second history line")
}

// TestComputeFootprint_ReferencesExcludePrefixSiblings is the boundary guard:
// the fixture deliberately seeds /Users/test/Projects/myproject-extras
// references in history.jsonl, .claude.json, and settings.json. A naive
// substring count would fold those into myproject's tally; the
// boundary-aware count must not.
func TestComputeFootprint_ReferencesExcludePrefixSiblings(t *testing.T) {
	targets := fixtureTargets(t)

	footprint, err := stats.ComputeFootprint(t.Context(), targets, fixtureProject)
	require.NoError(t, err)
	claudeFootprint := footprint.ByTool[0]

	assert.Equal(t, 5, referenceCount(t, &claudeFootprint, "history"),
		"history line for myproject-extras must not count toward myproject")
	assert.Equal(t, 4, referenceCount(t, &claudeFootprint, "config"),
		"the myproject-extras project key must not count toward myproject")
	assert.Equal(t, 4, referenceCount(t, &claudeFootprint, "settings"),
		"the myproject-extras marketplace path must not count toward myproject")
}

func TestComputeFootprint_DiskFootprintByCategory(t *testing.T) {
	targets := fixtureTargets(t)

	footprint, err := stats.ComputeFootprint(t.Context(), targets, fixtureProject)
	require.NoError(t, err)
	disk := footprint.ByTool[0].Disk

	// file-history spans two snapshot dirs (session …001 with 5 files, …003
	// with 2); the snapshots are sized but never read.
	assert.Equal(t, 7, diskUsage(t, disk, "file-history").Files)
	// sessions = 2 top-level transcripts + 3 session-subdir bodies + 1 sessions/*.json.
	assert.Equal(t, 6, diskUsage(t, disk, "sessions").Files)
	assert.Equal(t, 4, diskUsage(t, disk, "memory").Files)
	assert.Equal(t, 1, diskUsage(t, disk, "todos").Files)
	assert.Equal(t, 2, diskUsage(t, disk, "usage-data").Files)
	assert.Equal(t, 1, diskUsage(t, disk, "plugins-data").Files)
	assert.Equal(t, 1, diskUsage(t, disk, "tasks").Files)

	// history and config are shared globals: no per-project disk footprint.
	assert.Equal(t, tool.SizeCategory{Name: "history"}, diskUsage(t, disk, "history"))
	assert.Equal(t, tool.SizeCategory{Name: "config"}, diskUsage(t, disk, "config"))
}

func TestComputeFootprint_ReportsHistoryAndSessionCounts(t *testing.T) {
	targets := fixtureTargets(t)

	footprint, err := stats.ComputeFootprint(t.Context(), targets, fixtureProject)

	require.NoError(t, err)
	claudeFootprint := footprint.ByTool[0]
	assert.Positive(t, referenceCount(t, &claudeFootprint, "history entries"))
	assert.Positive(t, referenceCount(t, &claudeFootprint, "session files"))
}

func TestComputeFootprint_NotFoundIsAbsentNotError(t *testing.T) {
	targets := fixtureTargets(t)

	footprint, err := stats.ComputeFootprint(t.Context(), targets, "/no/such/project")
	require.NoError(t, err, "a project unknown to a tool must be reported as Absent, not fail the sweep")
	require.Len(t, footprint.ByTool, 1)
	assert.True(t, footprint.ByTool[0].Absent)
	assert.Empty(t, footprint.ByTool[0].Disk)
}

// TestComputeAllFootprints_RanksByBytesDescending checks the all-projects
// mode: every fixture project resolves a witness label (including the lossy
// "my project" spelling), and the ranking is byte-descending.
func TestComputeAllFootprints_RanksByBytesDescending(t *testing.T) {
	targets := fixtureTargets(t)

	footprints, err := stats.ComputeAllFootprints(t.Context(), targets)
	require.NoError(t, err)
	require.Len(t, footprints, 4)

	for index := 1; index < len(footprints); index++ {
		assert.GreaterOrEqual(t, footprints[index-1].Bytes, footprints[index].Bytes,
			"footprints must be ranked by bytes descending")
	}

	assert.Equal(t, fixtureProject, footprints[0].Label, "the richest project ranks first")
	assert.Equal(t, "claude", footprints[0].Tool)

	labels := make(map[string]bool, len(footprints))
	for _, footprint := range footprints {
		assert.True(t, footprint.Resolved, "every fixture project has a session witness")
		labels[footprint.Label] = true
	}
	assert.True(t, labels["/Users/test/Projects/my project"],
		"the witness label must use the space spelling, not the encoded dir name")
}

func TestComputeAllFootprints_EmptyHomeYieldsNone(t *testing.T) {
	home := &claude.Home{Dir: t.TempDir() + "/dotclaude", ConfigFile: t.TempDir() + "/dotclaude.json"}
	targets := []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}

	footprints, err := stats.ComputeAllFootprints(t.Context(), targets)
	require.NoError(t, err)
	assert.Empty(t, footprints)
}
