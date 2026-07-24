package importer_test

import (
	"archive/zip"
	"bytes"
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/tool"
)

// TestWithAllLocks_OrdersAndRefuses exercises importer.Run's multi-tool lock
// orchestration (the unexported withAllLocks): nested acquisition in
// registry order, and refusal when a target reports an active writer.
func TestWithAllLocks_OrdersAndRefuses(t *testing.T) {
	t.Run("nests each target's lock inside the previous one, in registry order", func(t *testing.T) {
		var mu sync.Mutex
		var events []string
		record := func(msg string) {
			mu.Lock()
			events = append(events, msg)
			mu.Unlock()
		}

		targets := newLockOrderTargets(t, []string{"alpha", "beta", "gamma"}, record, nil)
		toolSet := tool.NewSet(targets[0].Tool, targets[1].Tool, targets[2].Tool)
		body := buildEmptyManifestArchive(t)

		_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
			Source:     bytes.NewReader(body),
			Size:       int64(len(body)),
			TargetPath: "/Users/test/Projects/lock-order",
			Caps:       archive.DefaultCaps(),
		})
		require.NoError(t, err)

		assert.Equal(t, []string{
			"witness:alpha", "witness:beta", "witness:gamma",
			"witness:alpha", "witness:beta", "witness:gamma",
		}, events,
			"each target must be witnessed in registry order at lock time and again, same order, at the pre-promotion re-check")
		for _, target := range targets {
			assertLockFree(t, target.Workspace.LockPath())
		}
	})

	t.Run("refuses when a target reports an active writer, without touching later targets", func(t *testing.T) {
		var mu sync.Mutex
		var events []string
		record := func(msg string) {
			mu.Lock()
			events = append(events, msg)
			mu.Unlock()
		}

		activeByName := map[string][]tool.ActiveWriter{"beta": {{Pid: 4242, Cwd: "/writer/project"}}}
		targets := newLockOrderTargets(t, []string{"alpha", "beta", "gamma"}, record, activeByName)
		toolSet := tool.NewSet(targets[0].Tool, targets[1].Tool, targets[2].Tool)
		body := buildEmptyManifestArchive(t)

		_, err := importer.Run(t.Context(), toolSet, targets, &importer.Options{
			Source:     bytes.NewReader(body),
			Size:       int64(len(body)),
			TargetPath: "/Users/test/Projects/lock-refuse",
			Caps:       archive.DefaultCaps(),
		})

		var liveErr *lock.LiveSessionsError
		require.ErrorAs(t, err, &liveErr, "import must refuse via lock.LiveSessionsError rather than proceed")
		assert.Equal(t, []string{"witness:alpha", "witness:beta"}, events,
			"gamma must never be witnessed once beta's active writer refuses the import")
		assertLockFree(t, targets[0].Workspace.LockPath())
	})
}

// assertLockFree confirms path's advisory lock is currently unheld by
// taking and immediately releasing a sibling flock on it, the same
// per-fd-flock idiom internal/lock/lock_test.go uses to observe holder
// state from outside the package under test.
func assertLockFree(t *testing.T, path string) {
	t.Helper()
	sibling := flock.New(path)
	ok, err := sibling.TryLock()
	require.NoError(t, err)
	assert.True(t, ok, "%s must be released", path)
	if ok {
		require.NoError(t, sibling.Unlock())
	}
}

// lockOrderTool and lockOrderWorkspace are a minimal fake tool.Tool /
// tool.Workspace pair, modeled on internal/move/preflight_test.go's
// preflightTool/preflightWorkspace, whose ActiveWriters records call order
// and cross-checks that every earlier target's real lock file is still held
// — the same sibling-flock probe internal/lock/lock_test.go's
// TestWithLock_CallsFn uses — so withAllLocks's nesting is observed through
// the existing tool.Workspace seam rather than a hook added for this test.
type lockOrderTool struct{ name string }

func (fake *lockOrderTool) Name() string                        { return fake.name }
func (fake *lockOrderTool) DisplayName() string                 { return fake.name }
func (fake *lockOrderTool) Categories() []tool.Category         { return nil }
func (fake *lockOrderTool) Detect() (bool, error)               { return true, nil }
func (fake *lockOrderTool) Open(string) (tool.Workspace, error) { return nil, nil }
func (fake *lockOrderTool) ImplicitAnchorKeys() []string        { return nil }

type lockOrderWorkspace struct {
	t          *testing.T
	name       string
	lockPath   string
	priorPaths []string
	record     func(string)
	active     []tool.ActiveWriter
}

// newLockOrderTargets returns one tool.Target per name, in the given
// (registry) order, each holding a distinct real lock path. Every
// workspace's priorPaths holds the lock paths of every target before it in
// the slice, so its ActiveWriters can assert those are still held when it
// is called.
func newLockOrderTargets(t *testing.T, names []string, record func(string), activeByName map[string][]tool.ActiveWriter) []tool.Target {
	t.Helper()
	lockPaths := make([]string, len(names))
	for index, name := range names {
		lockPaths[index] = filepath.Join(t.TempDir(), name+".lock")
	}
	targets := make([]tool.Target, len(names))
	for index, name := range names {
		targets[index] = tool.Target{
			Tool: &lockOrderTool{name: name},
			Workspace: &lockOrderWorkspace{
				t:          t,
				name:       name,
				lockPath:   lockPaths[index],
				priorPaths: lockPaths[:index],
				record:     record,
				active:     activeByName[name],
			},
		}
	}
	return targets
}

func (workspace *lockOrderWorkspace) Root() string     { return workspace.lockPath }
func (workspace *lockOrderWorkspace) LockPath() string { return workspace.lockPath }

func (workspace *lockOrderWorkspace) ActiveWriters() ([]tool.ActiveWriter, error) {
	workspace.record("witness:" + workspace.name)
	for _, priorPath := range workspace.priorPaths {
		sibling := flock.New(priorPath)
		ok, err := sibling.TryLock()
		require.NoError(workspace.t, err)
		require.False(workspace.t, ok, "%s's lock must still be held while %s is witnessed", priorPath, workspace.name)
		if ok {
			_ = sibling.Unlock()
		}
	}
	return workspace.active, nil
}

func (workspace *lockOrderWorkspace) MoveSurfaces(tool.MoveRequest) ([]tool.Surface, error) {
	return nil, nil
}
func (workspace *lockOrderWorkspace) ResidualWarnings(tool.MoveRequest) ([]string, error) {
	return nil, nil
}
func (workspace *lockOrderWorkspace) Placeholders(string, map[string]bool) ([]manifest.Placeholder, error) {
	return nil, nil
}
func (workspace *lockOrderWorkspace) Export(context.Context, string, map[string]bool, *archive.Sink) (tool.ExportResult, error) {
	return tool.ExportResult{}, nil
}
func (workspace *lockOrderWorkspace) PreflightDirs(string) []string { return nil }
func (workspace *lockOrderWorkspace) ImplicitAnchors(string) (map[string]string, error) {
	return nil, nil
}
func (workspace *lockOrderWorkspace) Stage(context.Context, string, archive.Entry, map[string]string) ([]archive.Staged, error) {
	return nil, nil
}
func (workspace *lockOrderWorkspace) Finalize(context.Context, string, *archive.StagedSet) ([]string, error) {
	return nil, nil
}
func (workspace *lockOrderWorkspace) ReferenceSurfaces(context.Context, string) ([]tool.CountSurface, error) {
	return nil, nil
}
func (workspace *lockOrderWorkspace) DiskCategories(context.Context, string) ([]tool.SizeCategory, error) {
	return nil, nil
}
func (workspace *lockOrderWorkspace) EnumerateProjects(context.Context) ([]tool.ProjectInfo, error) {
	return nil, nil
}

// buildEmptyManifestArchive returns a well-formed archive with no <tool>
// blocks and no entries. Every target this test registers is then absent
// from the manifest, so runLocked reports them as skipped and never calls
// Stage/Finalize: only withAllLocks's lock orchestration, which runs before
// the manifest is even read, is exercised.
func buildEmptyManifestArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	_, err := archive.WriteMetadata(writer, &manifest.Metadata{})
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buf.Bytes()
}
