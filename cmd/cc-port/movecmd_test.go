package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
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
	assert.False(t, opts.DeepRewrite)
}

func TestParseMoveOptions_PropagatesFlagValues(t *testing.T) {
	cmd := newMoveCmdForTest(t)
	require.NoError(t, cmd.Flags().Set("refs-only", "true"))
	require.NoError(t, cmd.Flags().Set("deep", "true"))

	opts, err := parseMoveOptions(cmd, []string{
		"/Users/test/Projects/old", "/Users/test/Projects/new",
	})

	require.NoError(t, err)
	assert.True(t, opts.RefsOnly)
	assert.True(t, opts.DeepRewrite)
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
	cmd.Flags().Bool("deep", false, "")
	return cmd
}

func TestRunMoveDryRun_PrintsPerToolSurfacesAndApplyHint(t *testing.T) {
	home := testutil.SetupFixture(t)
	targets := []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
	var stdout bytes.Buffer

	err := runMoveDryRun(t.Context(), &stdout, targets, move.Options{
		OldPath: "/Users/test/Projects/myproject",
		NewPath: "/Users/test/Projects/relocated",
	})

	require.NoError(t, err)
	output := stdout.String()
	assert.Contains(t, output, "[claude]")
	assert.Contains(t, output, "Run with --apply to execute.")
}

func TestRunMoveDryRun_WarnsAboutActiveWriter(t *testing.T) {
	home := testutil.SetupFixture(t)
	require.NoError(t, os.MkdirAll(home.SessionsDir(), 0o750))
	writer, err := json.Marshal(claude.SessionFile{Cwd: testutil.FixtureProjectPath(), Pid: os.Getpid()})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home.SessionsDir(), "live.json"), writer, 0o600))
	targets := []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
	var stdout bytes.Buffer

	err = runMoveDryRun(t.Context(), &stdout, targets, move.Options{
		OldPath: testutil.FixtureProjectPath(), NewPath: testutil.FixtureProjectPath() + "-renamed",
	})

	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "active Claude Code writer")
	assert.Contains(t, stdout.String(), "pid=")
}
