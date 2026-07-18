package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/testutil"
	"github.com/it-bens/cc-port/internal/tool"
	"github.com/it-bens/cc-port/internal/tool/claude"
)

type codexOnlyTool struct{}

func (*codexOnlyTool) Name() string                 { return "codex" }
func (*codexOnlyTool) DisplayName() string          { return "OpenAI Codex" }
func (*codexOnlyTool) Categories() []tool.Category  { return nil }
func (*codexOnlyTool) Detect() (bool, error)        { return true, nil }
func (*codexOnlyTool) ImplicitAnchorKeys() []string { return nil }
func (*codexOnlyTool) Open(string) (tool.Workspace, error) {
	return nil, assert.AnError
}

type codexOnlyWorkspace struct{}

func (*codexOnlyWorkspace) Root() string                                { return "/codex" }
func (*codexOnlyWorkspace) LockPath() string                            { return "" }
func (*codexOnlyWorkspace) ActiveWriters() ([]tool.ActiveWriter, error) { return nil, nil }
func (*codexOnlyWorkspace) MoveSurfaces(tool.MoveRequest) ([]tool.Surface, error) {
	return []tool.Surface{{Name: "state", Plan: func(context.Context) (tool.SurfaceResult, error) { return tool.SurfaceResult{}, nil }}}, nil
}
func (*codexOnlyWorkspace) ResidualWarnings(tool.MoveRequest) ([]string, error) { return nil, nil }
func (*codexOnlyWorkspace) Placeholders(string, map[string]bool) ([]manifest.Placeholder, error) {
	return nil, assert.AnError
}
func (*codexOnlyWorkspace) Export(context.Context, string, map[string]bool, *archive.Sink) (tool.ExportResult, error) {
	return tool.ExportResult{}, assert.AnError
}
func (*codexOnlyWorkspace) PreflightDirs(string) []string { return nil }
func (*codexOnlyWorkspace) ImplicitAnchors(string) (map[string]string, error) {
	return nil, assert.AnError
}
func (*codexOnlyWorkspace) Stage(context.Context, string, archive.Entry, map[string]string) ([]archive.Staged, error) {
	return nil, assert.AnError
}
func (*codexOnlyWorkspace) Finalize(context.Context, string, *archive.StagedSet) ([]string, error) {
	return nil, assert.AnError
}
func (*codexOnlyWorkspace) ReferenceSurfaces(context.Context, string) ([]tool.CountSurface, error) {
	return nil, assert.AnError
}
func (*codexOnlyWorkspace) DiskCategories(context.Context, string) ([]tool.SizeCategory, error) {
	return nil, assert.AnError
}
func (*codexOnlyWorkspace) EnumerateProjects(context.Context) ([]tool.ProjectInfo, error) {
	return nil, assert.AnError
}

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

func TestRunMoveDryRun_CodexOnlyWarnsNoPhysicalProjectMove(t *testing.T) {
	targets := []tool.Target{{Tool: &codexOnlyTool{}, Workspace: &codexOnlyWorkspace{}}}
	var stdout bytes.Buffer

	err := runMoveDryRun(t.Context(), &stdout, targets, move.Options{OldPath: "/old", NewPath: "/new"})

	require.NoError(t, err)
	assert.Contains(t, stdout.String(), move.NoPhysicalMoveWarning)
}

func TestRenderApplyResultPrintsResidualWarnings(t *testing.T) {
	var stdout bytes.Buffer

	result := &move.ApplyResult{ByTool: []move.ToolResult{{
		Tool: "codex", Success: true,
		Warnings: []string{"codex-dev.db contains path references and is never rewritten"},
	}}}
	renderApplyResult(&stdout, result)

	assert.Contains(t, stdout.String(), "! codex-dev.db contains path references and is never rewritten")
}

func TestRenderApplyResult_PrintsNoPhysicalProjectMoveWarning(t *testing.T) {
	var stdout bytes.Buffer

	renderApplyResult(&stdout, &move.ApplyResult{Warnings: []string{move.NoPhysicalMoveWarning}})

	assert.Contains(t, stdout.String(), move.NoPhysicalMoveWarning)
}
