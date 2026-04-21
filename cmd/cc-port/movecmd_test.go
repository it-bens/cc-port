package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
