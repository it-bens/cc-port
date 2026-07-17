// Package codex implements the OpenAI Codex tool adapter.
package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/tool"
)

// toolName is Codex's wire identity: archive prefix, manifest
// <tool name=…> attribute, and generated --codex-home flag.
const toolName = "codex"

var (
	_ tool.Tool      = (*Adapter)(nil)
	_ tool.Workspace = (*Workspace)(nil)
)

// errNotImplementedYet is returned by every Exporter/Importer/Auditor
// method: this bundle implements the move side only (export, import, and
// stats land in the next bundle). Loud and explicit rather than a silent
// empty success, since nothing calls these methods yet — Codex is not
// registered in cmd/cc-port/tools.go until that bundle lands.
var errNotImplementedYet = errors.New("codex: not implemented until the export/import/stats bundle")

// Adapter implements tool.Tool for OpenAI Codex. Environment lookups,
// process enumeration, and the clock enter via these fields rather than
// free in-line calls (spec §1 construction seams), so Detect, Open, and
// the witness Open returns are all testable without global mutation.
type Adapter struct {
	getenv        func(string) string
	listProcesses ProcessLister
	now           func() time.Time
	pidAlive      func(int) bool
}

// New returns the Codex tool adapter, wired to the real environment, the
// real process table, and the wall clock.
func New() *Adapter {
	return NewAdapter(os.Getenv, listSystemProcesses, time.Now)
}

// NewAdapter returns a Codex tool adapter with explicit seams. Production
// callers use New; tests supply a fake getenv, process lister, or clock to
// drive Detect, Open, and the witness without touching live machine state.
func NewAdapter(getenv func(string) string, listProcesses ProcessLister, now func() time.Time) *Adapter {
	return &Adapter{getenv: getenv, listProcesses: listProcesses, now: now, pidAlive: processAlive}
}

// Name implements tool.Tool.
func (*Adapter) Name() string { return toolName }

// DisplayName implements tool.Tool.
func (*Adapter) DisplayName() string { return "OpenAI Codex" }

// Categories implements tool.Tool.
func (*Adapter) Categories() []tool.Category { return categories }

// ImplicitAnchorKeys implements tool.Tool. Codex declares no implicit
// placeholder anchors in this bundle; export/import land in the next one.
func (*Adapter) ImplicitAnchorKeys() []string { return nil }

// Detect implements tool.Tool: it reports whether the default ~/.codex
// directory exists, independent of any --codex-home override.
func (adapter *Adapter) Detect() (bool, error) {
	dir, err := defaultCodexHome(adapter.getenv)
	if err != nil {
		return false, err
	}
	return dirExists(dir)
}

// Open implements tool.Tool. An explicit override must already exist, be a
// directory, and canonicalize (spec §6.1); the default location may be
// absent, in which case Open reports tool.ErrToolAbsent rather than
// fabricating a Workspace over state that was never written.
func (adapter *Adapter) Open(override string) (tool.Workspace, error) {
	if override != "" {
		dir, err := canonicalizeExistingDir(override)
		if err != nil {
			return nil, fmt.Errorf("codex home override %q: %w", override, err)
		}
		return adapter.openAt(dir)
	}

	dir, err := defaultCodexHome(adapter.getenv)
	if err != nil {
		return nil, err
	}
	exists, err := dirExists(dir)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, tool.ErrToolAbsent
	}
	return adapter.openAt(dir)
}

func (adapter *Adapter) openAt(dir string) (tool.Workspace, error) {
	home, err := newHome(dir, adapter.getenv)
	if err != nil {
		return nil, err
	}
	return newWorkspace(home, adapter.getenv, adapter.listProcesses, adapter.now, adapter.pidAlive), nil
}

func dirExists(dir string) (bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s exists but is not a directory", dir)
	}
	return true, nil
}

// NewWorkspace returns a Workspace bound to home, carrying the same seams
// as the Adapter that resolved it. Exported for fixtures that already hold
// a *Home and need a Workspace without going through Adapter.Open's
// flag-parsing path.
func NewWorkspace(home *Home, getenv func(string) string, listProcesses ProcessLister, now func() time.Time) *Workspace {
	return newWorkspace(home, getenv, listProcesses, now, processAlive)
}

func newWorkspace(home *Home, getenv func(string) string, listProcesses ProcessLister, now func() time.Time, pidAlive func(int) bool) *Workspace {
	return &Workspace{home: home, getenv: getenv, listProcesses: listProcesses, now: now, pidAlive: pidAlive}
}

// Workspace implements tool.Workspace for one resolved Codex home.
type Workspace struct {
	home          *Home
	getenv        func(string) string
	listProcesses ProcessLister
	now           func() time.Time
	pidAlive      func(int) bool
	applyWarnings []string
	warningMutex  sync.Mutex
}

// Root implements tool.Workspace.
func (workspace *Workspace) Root() string { return workspace.home.Dir }

// LockPath implements tool.Workspace.
func (workspace *Workspace) LockPath() string {
	return filepath.Join(workspace.home.Dir, lock.FileName)
}

// Placeholders implements tool.Exporter. Deferred to the export/import bundle.
func (*Workspace) Placeholders(string, map[string]bool) ([]manifest.Placeholder, error) {
	return nil, errNotImplementedYet
}

// Export implements tool.Exporter. Deferred to the export/import bundle.
func (*Workspace) Export(context.Context, string, map[string]bool, *archive.Sink) (tool.ExportResult, error) {
	return tool.ExportResult{}, errNotImplementedYet
}

// PreflightDirs implements tool.Importer. Deferred to the export/import
// bundle: Stage does not exist yet, so there is nothing to preflight.
func (*Workspace) PreflightDirs(string) []string { return nil }

// ImplicitAnchors implements tool.Importer. Deferred to the export/import bundle.
func (*Workspace) ImplicitAnchors(string) (map[string]string, error) {
	return nil, errNotImplementedYet
}

// Stage implements tool.Importer. Deferred to the export/import bundle.
func (*Workspace) Stage(context.Context, string, archive.Entry, map[string]string) ([]archive.Staged, error) {
	return nil, errNotImplementedYet
}

// Finalize implements tool.Importer. Deferred to the export/import bundle.
func (*Workspace) Finalize(context.Context, string, *archive.StagedSet) error {
	return errNotImplementedYet
}

// ReferenceSurfaces implements tool.Auditor. Deferred to the stats bundle.
func (*Workspace) ReferenceSurfaces(string) ([]tool.CountSurface, error) {
	return nil, errNotImplementedYet
}

// DiskCategories implements tool.Auditor. Deferred to the stats bundle.
func (*Workspace) DiskCategories(string) ([]tool.SizeCategory, error) {
	return nil, errNotImplementedYet
}

// EnumerateProjects implements tool.Auditor. Deferred to the stats bundle.
func (*Workspace) EnumerateProjects() ([]tool.ProjectInfo, error) {
	return nil, errNotImplementedYet
}
