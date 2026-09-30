package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/lock"
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
func (*codexOnlyWorkspace) MoveSurfaces(context.Context, tool.MoveRequest) ([]tool.Surface, error) {
	return []tool.Surface{{Name: "state", Plan: func(context.Context) (tool.SurfaceResult, error) { return tool.SurfaceResult{}, nil }}}, nil
}
func (*codexOnlyWorkspace) ResidualWarnings(context.Context, tool.MoveRequest) ([]string, error) {
	return nil, nil
}
func (*codexOnlyWorkspace) Placeholders(context.Context, string, map[string]bool) ([]manifest.Placeholder, error) {
	return nil, assert.AnError
}
func (*codexOnlyWorkspace) Export(context.Context, string, map[string]bool, *archive.Sink) (tool.ExportResult, error) {
	return tool.ExportResult{}, assert.AnError
}
func (*codexOnlyWorkspace) PreflightDirs(string) []string { return nil }
func (*codexOnlyWorkspace) ImplicitAnchors(string) (map[string]string, error) {
	return nil, assert.AnError
}
func (*codexOnlyWorkspace) MCPServers() ([]tool.MCPServer, error) {
	return nil, assert.AnError
}
func (*codexOnlyWorkspace) ArchiveMCPServers(archive.Entry, map[string]string) ([]tool.MCPServer, error) {
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

func (*codexOnlyWorkspace) AuditWarnings(context.Context) ([]string, error) { return nil, nil }

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
	assert.Contains(t, output, "References (25 changes)")
	assert.Contains(t, output, "history                  4")
	assert.Contains(t, output, "Run with --apply to execute.")
}

func TestRunMoveDryRun_WarnsAboutActiveWriter(t *testing.T) {
	home := testutil.SetupFixture(t)
	writeLiveClaudeSession(t, home, testutil.FixtureProjectPath())
	targets := []tool.Target{{Tool: claude.New(), Workspace: claude.NewWorkspace(home)}}
	var stdout bytes.Buffer

	err := runMoveDryRun(t.Context(), &stdout, targets, move.Options{
		OldPath: testutil.FixtureProjectPath(), NewPath: testutil.FixtureProjectPath() + "-renamed",
	})

	require.NoError(t, err)
	assert.Contains(t, stdout.String(),
		fmt.Sprintf("    ! active Claude Code writer: pid=%d cwd=%q\n", os.Getpid(), testutil.FixtureProjectPath()))
}

func TestRunMoveDryRun_WarnsAboutBusyDatabase(t *testing.T) {
	targets := []tool.Target{{Tool: &codexOnlyTool{}, Workspace: &liveWriterWorkspace{
		writers: []tool.ActiveWriter{{Detail: "busy database state_5.sqlite"}},
	}}}
	var stdout bytes.Buffer

	err := runMoveDryRun(t.Context(), &stdout, targets, move.Options{OldPath: "/old", NewPath: "/new"})

	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "    ! active OpenAI Codex writer: busy database state_5.sqlite\n")
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

func TestMoveApplyRefusalOnLiveSessionNamesTheOverrideFlag(t *testing.T) {
	home := testutil.SetupFixture(t)
	writeLiveClaudeSession(t, home, "/Users/test/Projects/other")

	_, err := executeCmd(t,
		"move", testutil.FixtureProjectPath(), "/Users/test/Projects/relocated",
		"--tool", "claude", "--claude-home", home.Dir, "--refs-only", "--apply",
	)

	_, ok := errors.AsType[*lock.LiveSessionsError](err)
	require.True(t, ok, "err = %v, want *lock.LiveSessionsError", err)
	assert.True(t, strings.HasSuffix(err.Error(), "; pass --ignore-live-sessions to proceed anyway"), err.Error())
}

func TestMoveApplyIgnoringLiveSessionsPrintsIgnoredWritersWhenTheMoveFails(t *testing.T) {
	home := testutil.SetupFixture(t)
	ignoredLine := writeLiveClaudeSession(t, home, "/Users/test/Projects/other")
	// The fixture project has no directory on disk, so the physical move
	// fails when the surfaces apply, after the witness ran.
	newPath := filepath.Join(t.TempDir(), "relocated")

	stderr, err := executeCmd(t,
		"move", testutil.FixtureProjectPath(), newPath,
		"--tool", "claude", "--claude-home", home.Dir, "--apply", "--ignore-live-sessions",
	)

	require.ErrorContains(t, err, "one or more tools failed to move")
	assert.Contains(t, stderr, ignoredLine)
}

func TestMoveApplyIgnoringLiveSessionsPrintsIgnoredWritersWhenTheMoveSucceeds(t *testing.T) {
	home := testutil.SetupFixture(t)
	ignoredLine := writeLiveClaudeSession(t, home, "/Users/test/Projects/other")

	// --refs-only skips the physical project-directory move, which the fixture
	// project's absence from disk would otherwise fail.
	stderr, err := executeCmd(t,
		"move", testutil.FixtureProjectPath(), testutil.FixtureProjectPath()+"-renamed",
		"--tool", "claude", "--claude-home", home.Dir, "--refs-only", "--apply", "--ignore-live-sessions",
	)

	require.NoError(t, err)
	assert.Contains(t, stderr, ignoredLine)
}

// Move renders its result table before the ignored writers, so this case pins
// that the render error reaches the caller instead of being dropped; import and
// pull are the commands whose later output a failed render could suppress.
func TestMoveApplyIgnoringLiveSessionsPrintsTheResultWhenTheIgnoredWriterLinesFail(t *testing.T) {
	home := testutil.SetupFixture(t)
	writeLiveClaudeSession(t, home, "/Users/test/Projects/other")
	failingStderr := &ignoredWriterFailer{}

	stdout, err := executeCmdStreams(t, failingStderr,
		"move", testutil.FixtureProjectPath(), testutil.FixtureProjectPath()+"-renamed",
		"--tool", "claude", "--claude-home", home.Dir, "--refs-only", "--apply", "--ignore-live-sessions",
	)

	require.ErrorContains(t, err, "write ignored writer")
	assert.Contains(t, stdout, "[claude] OK")
}
