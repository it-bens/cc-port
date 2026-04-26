package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/scan"
	"github.com/it-bens/cc-port/internal/testutil"
)

func TestParseMoveOptions_ResolvesPaths(t *testing.T) {
	cmd := newMoveCmdForTest(t)

	opts, err := parseMoveOptions(cmd, []string{
		"/Users/test/Projects/old", "/Users/test/Projects/new",
	})

	require.NoError(t, err)
	assert.Equal(t, "/Users/test/Projects/old", opts.OldPath)
	assert.Equal(t, "/Users/test/Projects/new", opts.NewPath)
	assert.False(t, opts.RefsOnly)
	assert.False(t, opts.RewriteTranscripts)
}

func TestParseMoveOptions_PropagatesFlagValues(t *testing.T) {
	cmd := newMoveCmdForTest(t)
	require.NoError(t, cmd.Flags().Set("refs-only", "true"))
	require.NoError(t, cmd.Flags().Set("rewrite-transcripts", "true"))

	opts, err := parseMoveOptions(cmd, []string{
		"/Users/test/Projects/old", "/Users/test/Projects/new",
	})

	require.NoError(t, err)
	assert.True(t, opts.RefsOnly)
	assert.True(t, opts.RewriteTranscripts)
}

func TestParseMoveOptions_RejectsIdenticalPaths(t *testing.T) {
	cmd := newMoveCmdForTest(t)

	_, err := parseMoveOptions(cmd, []string{
		"/Users/test/Projects/same", "/Users/test/Projects/same",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "old and new paths are identical")
}

func newMoveCmdForTest(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().Bool("apply", false, "")
	cmd.Flags().Bool("refs-only", false, "")
	cmd.Flags().Bool("rewrite-transcripts", false, "")
	return cmd
}

func TestRenderReferencesBlockPrintsHeaderWithChangeCount(t *testing.T) {
	plan := &move.Plan{
		ReplacementsByCategory: map[string]int{"history": 2, "sessions": 3, "settings": 1},
	}
	var stdout bytes.Buffer

	renderReferencesBlock(&stdout, plan)

	assert.Contains(t, stdout.String(), "References (6 changes)")
}

func TestRenderReferencesBlockEmitsLineForEachNonZeroCategory(t *testing.T) {
	plan := &move.Plan{
		ReplacementsByCategory: map[string]int{"history": 2, "sessions": 3, "settings": 1},
	}
	var stdout bytes.Buffer

	renderReferencesBlock(&stdout, plan)

	output := stdout.String()
	assert.Contains(t, output, "history.jsonl")
	assert.Contains(t, output, "sessions/*.json")
	assert.Contains(t, output, "settings.json")
}

func TestRenderReferencesBlockSkipsZeroCountCategories(t *testing.T) {
	plan := &move.Plan{
		ReplacementsByCategory: map[string]int{"history": 0, "sessions": 5},
	}
	var stdout bytes.Buffer

	renderReferencesBlock(&stdout, plan)

	assert.NotContains(t, stdout.String(), "history.jsonl")
}

func TestRenderReferencesBlockExcludesFileHistorySnapshotsFromCount(t *testing.T) {
	plan := &move.Plan{
		ReplacementsByCategory: map[string]int{"file-history-snapshots": 99},
	}
	var stdout bytes.Buffer

	renderReferencesBlock(&stdout, plan)

	assert.Contains(t, stdout.String(), "References (0 changes)")
}

func TestRenderReferencesBlockEmitsConfigBlockRekeyLine(t *testing.T) {
	plan := &move.Plan{
		ReplacementsByCategory: map[string]int{},
		ConfigBlockRekey:       true,
	}
	var stdout bytes.Buffer

	renderReferencesBlock(&stdout, plan)

	output := stdout.String()
	assert.Contains(t, output, "References (1 changes)")
	assert.Contains(t, output, "~/.claude.json")
	assert.Contains(t, output, "re-key project block")
}

func TestRenderPlanWarningsPrintsNoWarningsLineWhenClean(t *testing.T) {
	plan := &move.Plan{}
	var stdout bytes.Buffer

	renderPlanWarnings(&stdout, plan)

	assert.Contains(t, stdout.String(), "No rules file warnings")
}

func TestRenderPlanWarningsReportsMalformedHistoryLines(t *testing.T) {
	plan := &move.Plan{HistoryMalformedLines: []int{4, 17}}
	var stdout bytes.Buffer

	renderPlanWarnings(&stdout, plan)

	output := stdout.String()
	assert.Contains(t, output, "2 malformed line")
	assert.Contains(t, output, "[4 17]")
}

func TestRenderPlanWarningsReportsRulesFileMatches(t *testing.T) {
	plan := &move.Plan{
		RulesWarnings: []scan.Warning{
			{File: "go-style.md", Line: 12},
			{File: "review-checklist.md", Line: 47},
		},
	}
	var stdout bytes.Buffer

	renderPlanWarnings(&stdout, plan)

	output := stdout.String()
	assert.Contains(t, output, "go-style.md")
	assert.Contains(t, output, "(line 12)")
	assert.Contains(t, output, "review-checklist.md")
	assert.Contains(t, output, "(line 47)")
}

func TestReportActiveSessionOnSourceSilentWhenNoneActive(t *testing.T) {
	home := testutil.SetupFixture(t)
	withMoveSeams(t, func(*claude.Home) ([]lock.ActiveSession, error) {
		return nil, nil
	})
	var stderr bytes.Buffer

	err := reportActiveSessionOnSource(&stderr, home, "/Users/test/Projects/myproject")

	require.NoError(t, err)
	assert.Empty(t, stderr.String())
}

func TestReportActiveSessionOnSourceSilentWhenActiveSessionElsewhere(t *testing.T) {
	home := testutil.SetupFixture(t)
	withMoveSeams(t, func(*claude.Home) ([]lock.ActiveSession, error) {
		return []lock.ActiveSession{{Pid: 4242, Cwd: "/Users/test/Projects/other"}}, nil
	})
	var stderr bytes.Buffer

	err := reportActiveSessionOnSource(&stderr, home, "/Users/test/Projects/myproject")

	require.NoError(t, err)
	assert.Empty(t, stderr.String())
}

func TestReportActiveSessionOnSourcePrintsNoteWhenActiveSessionMatches(t *testing.T) {
	home := testutil.SetupFixture(t)
	withMoveSeams(t, func(*claude.Home) ([]lock.ActiveSession, error) {
		return []lock.ActiveSession{{Pid: 4242, Cwd: "/Users/test/Projects/myproject"}}, nil
	})
	var stderr bytes.Buffer

	err := reportActiveSessionOnSource(&stderr, home, "/Users/test/Projects/myproject")

	require.NoError(t, err)
	output := stderr.String()
	assert.Contains(t, output, "pid 4242")
	assert.Contains(t, output, "--apply will refuse")
}

func TestReportActiveSessionOnSourceWrapsLockError(t *testing.T) {
	sentinel := errors.New("simulated FindActive failure")
	home := testutil.SetupFixture(t)
	withMoveSeams(t, func(*claude.Home) ([]lock.ActiveSession, error) {
		return nil, sentinel
	})
	var stderr bytes.Buffer

	err := reportActiveSessionOnSource(&stderr, home, "/Users/test/Projects/myproject")

	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "check active sessions")
}

// withMoveSeams swaps the package-level findActive seam for the duration
// of t and restores the original via t.Cleanup. Mirrors withSeams in
// internal/ui/prompt_test.go.
func withMoveSeams(t *testing.T, find func(*claude.Home) ([]lock.ActiveSession, error)) {
	t.Helper()
	original := findActive
	t.Cleanup(func() { findActive = original })
	findActive = find
}
