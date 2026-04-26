package main

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/move"
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
