// Package codex implements the OpenAI Codex tool adapter.
package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/tool"
)

// toolName is Codex's wire identity: archive prefix, manifest
// <tool name=…> attribute, and generated --codex-home flag.
const toolName = "codex"

var (
	_ tool.Tool      = (*Adapter)(nil)
	_ tool.Workspace = (*Workspace)(nil)
)

// Adapter implements tool.Tool for OpenAI Codex. Environment lookups and
// process enumeration enter via these fields rather than free in-line
// calls (spec §1 construction seams), so Detect, Open, and the witness
// Open returns are all testable without global mutation.
type Adapter struct {
	getenv        func(string) string
	listProcesses ProcessLister
}

// New returns the Codex tool adapter, wired to the real environment and the
// real process table.
func New() *Adapter {
	return NewAdapter(os.Getenv, listSystemProcesses)
}

// NewAdapter returns a Codex tool adapter with explicit seams. Production
// callers use New; tests supply a fake getenv or process lister to drive
// Detect, Open, and the witness without touching live machine state.
func NewAdapter(getenv func(string) string, listProcesses ProcessLister) *Adapter {
	return &Adapter{getenv: getenv, listProcesses: listProcesses}
}

// Name implements tool.Tool.
func (*Adapter) Name() string { return toolName }

// DisplayName implements tool.Tool.
func (*Adapter) DisplayName() string { return "OpenAI Codex" }

// Categories implements tool.Tool.
func (*Adapter) Categories() []tool.Category { return categories }

// ImplicitAnchorKeys implements tool.Tool. Codex home and project anchors are
// resolved to the destination workspace during import.
func (*Adapter) ImplicitAnchorKeys() []string { return []string{codexHomeKey, codexProjectPathKey} }

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
	return newWorkspace(home, adapter.getenv, adapter.listProcesses), nil
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
func NewWorkspace(home *Home, getenv func(string) string, listProcesses ProcessLister) *Workspace {
	return newWorkspace(home, getenv, listProcesses)
}

func newWorkspace(
	home *Home,
	getenv func(string) string,
	listProcesses ProcessLister,
) *Workspace {
	return &Workspace{home: home, getenv: getenv, listProcesses: listProcesses}
}

// Workspace implements tool.Workspace for one resolved Codex home.
type Workspace struct {
	home           *Home
	getenv         func(string) string
	listProcesses  ProcessLister
	applyWarnings  []string
	warningMutex   sync.Mutex
	historyAppends [][]byte
	indexAppends   [][]byte
	sidecarAppends [][]byte
	rolloutsStaged bool
}

// Root implements tool.Workspace.
func (workspace *Workspace) Root() string { return workspace.home.Dir }

// LockPath implements tool.Workspace.
func (workspace *Workspace) LockPath() string {
	return filepath.Join(workspace.home.Dir, lock.FileName)
}
