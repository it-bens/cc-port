package move_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/move"
	"github.com/it-bens/cc-port/internal/tool"
)

func TestApply_PreflightsEveryWitnessBeforeAnySurfaceApply(t *testing.T) {
	events := []string{}
	first := newPreflightTarget(t, "first", &events, 0)
	second := newPreflightTarget(t, "second", &events, 0)

	result, err := move.Apply(t.Context(), []tool.Target{first, second}, move.Options{OldPath: "/old", NewPath: "/new"})

	require.NoError(t, err)
	assert.False(t, result.Failed())
	assert.Equal(t, []string{
		"surface:first", "witness:first", "surface:second", "witness:second", "witness:first", "witness:second", "apply:first", "apply:second",
	}, events)
}

func TestApply_WriterStartedAfterPreflightPreventsAnyApply(t *testing.T) {
	events := []string{}
	first := newPreflightTarget(t, "first", &events, 2)
	second := newPreflightTarget(t, "second", &events, 0)

	_, err := move.Apply(context.Background(), []tool.Target{first, second}, move.Options{OldPath: "/old", NewPath: "/new"})

	require.Error(t, err)
	assert.Equal(t, []string{
		"surface:first", "witness:first", "surface:second", "witness:second", "witness:first", "witness:second",
	}, events)
	var liveError *lock.LiveSessionsError
	require.ErrorAs(t, err, &liveError)
	assert.Equal(t, []tool.ActiveWriter{{Pid: 1, Cwd: "/writer"}}, liveError.Sessions)
}

func TestApply_SecondWitnessRefusalPreventsFirstMutation(t *testing.T) {
	events := []string{}
	first := newPreflightTarget(t, "first", &events, 0)
	second := newPreflightTarget(t, "second", &events, 1)

	_, err := move.Apply(context.Background(), []tool.Target{first, second}, move.Options{OldPath: "/old", NewPath: "/new"})

	require.Error(t, err)
	assert.Equal(t, []string{"surface:first", "witness:first", "surface:second", "witness:second"}, events)
}

type preflightTool struct{ name string }

func (preflight *preflightTool) Name() string                        { return preflight.name }
func (preflight *preflightTool) DisplayName() string                 { return preflight.name }
func (preflight *preflightTool) Categories() []tool.Category         { return nil }
func (preflight *preflightTool) Detect() (bool, error)               { return true, nil }
func (preflight *preflightTool) Open(string) (tool.Workspace, error) { return nil, nil }
func (preflight *preflightTool) ImplicitAnchorKeys() []string        { return nil }

type preflightWorkspace struct {
	name     string
	lockPath string
	events   *[]string
	// refuseFrom picks the call the witness turns live on: 0 keeps it quiet,
	// 1 reports a live writer from the lock-time call, 2 or more from that
	// call onward — 2 models a session started after the lock-time witness.
	refuseFrom   int
	witnessCalls int
}

func newPreflightTarget(t *testing.T, name string, events *[]string, refuseFrom int) tool.Target {
	t.Helper()
	return tool.Target{
		Tool:      &preflightTool{name: name},
		Workspace: &preflightWorkspace{name: name, lockPath: filepath.Join(t.TempDir(), name+".lock"), events: events, refuseFrom: refuseFrom},
	}
}

func (workspace *preflightWorkspace) Root() string     { return workspace.lockPath }
func (workspace *preflightWorkspace) LockPath() string { return workspace.lockPath }
func (workspace *preflightWorkspace) ActiveWriters() ([]tool.ActiveWriter, error) {
	*workspace.events = append(*workspace.events, "witness:"+workspace.name)
	workspace.witnessCalls++
	if workspace.refuseFrom > 0 && workspace.witnessCalls >= workspace.refuseFrom {
		return []tool.ActiveWriter{{Pid: 1, Cwd: "/writer"}}, nil
	}
	return nil, nil
}
func (workspace *preflightWorkspace) MoveSurfaces(context.Context, tool.MoveRequest) ([]tool.Surface, error) {
	*workspace.events = append(*workspace.events, "surface:"+workspace.name)
	surface := tool.Surface{
		Name: "one",
		Plan: func(context.Context) (tool.SurfaceResult, error) { return tool.SurfaceResult{}, nil },
		Apply: func(context.Context, *tool.Restorer) (tool.SurfaceResult, error) {
			*workspace.events = append(*workspace.events, "apply:"+workspace.name)
			return tool.SurfaceResult{Count: 1}, nil
		},
	}
	return []tool.Surface{surface}, nil
}
func (workspace *preflightWorkspace) ResidualWarnings(context.Context, tool.MoveRequest) ([]string, error) {
	return nil, nil
}
func (workspace *preflightWorkspace) Placeholders(context.Context, string, map[string]bool) ([]manifest.Placeholder, error) {
	return nil, nil
}
func (workspace *preflightWorkspace) Export(context.Context, string, map[string]bool, *archive.Sink) (tool.ExportResult, error) {
	return tool.ExportResult{}, nil
}
func (workspace *preflightWorkspace) PreflightDirs(string) []string { return nil }
func (workspace *preflightWorkspace) ImplicitAnchors(string) (map[string]string, error) {
	return nil, nil
}
func (workspace *preflightWorkspace) MCPServers() ([]tool.MCPServer, error) {
	return nil, nil
}
func (workspace *preflightWorkspace) ArchiveMCPServers(archive.Entry, map[string]string) ([]tool.MCPServer, error) {
	return nil, nil
}
func (workspace *preflightWorkspace) Stage(context.Context, string, archive.Entry, map[string]string) ([]archive.Staged, error) {
	return nil, nil
}
func (workspace *preflightWorkspace) Finalize(context.Context, string, *archive.StagedSet) ([]string, error) {
	return nil, nil
}
func (workspace *preflightWorkspace) ReferenceSurfaces(context.Context, string) ([]tool.CountSurface, error) {
	return nil, nil
}
func (workspace *preflightWorkspace) DiskCategories(context.Context, string) ([]tool.SizeCategory, error) {
	return nil, nil
}
func (workspace *preflightWorkspace) EnumerateProjects(context.Context) ([]tool.ProjectInfo, error) {
	return nil, nil
}

func (*preflightWorkspace) AuditWarnings(context.Context) ([]string, error) { return nil, nil }
