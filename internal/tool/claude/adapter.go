package claude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/tool"
)

// toolName is Claude Code's wire identity: archive prefix, manifest
// <tool name=…> attribute, and generated --claude-home flag.
const toolName = "claude"

var (
	_ tool.Tool      = (*Adapter)(nil)
	_ tool.Workspace = (*Workspace)(nil)
)

// Adapter implements tool.Tool for Claude Code.
type Adapter struct{}

// New returns the Claude Code tool adapter.
func New() *Adapter { return &Adapter{} }

// Name implements tool.Tool.
func (*Adapter) Name() string { return toolName }

// DisplayName implements tool.Tool.
func (*Adapter) DisplayName() string { return "Claude Code" }

// Categories implements tool.Tool.
func (*Adapter) Categories() []tool.Category { return categories }

// ImplicitAnchorKeys implements tool.Tool.
func (*Adapter) ImplicitAnchorKeys() []string {
	return []string{projectPathKey, homePathKey, projectDirKey}
}

// Detect implements tool.Tool: it reports whether the default ~/.claude
// directory exists, independent of any --claude-home override.
func (*Adapter) Detect() (bool, error) {
	home, err := NewHome("")
	if err != nil {
		return false, err
	}
	if _, statErr := os.Stat(home.Dir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", home.Dir, statErr)
	}
	return true, nil
}

// Open implements tool.Tool. Claude's home directory may not exist yet — a
// Workspace bound to a not-yet-created home is valid; mutating commands
// create it lazily on first write, matching Claude Code's own behavior.
func (*Adapter) Open(override string) (tool.Workspace, error) {
	home, err := NewHome(override)
	if err != nil {
		return nil, err
	}
	return NewWorkspace(home), nil
}

// NewWorkspace returns a Workspace bound to home. Exported for tests and
// fixtures that already hold a *Home (e.g. a staged fixture directory)
// and need a Workspace without going through Adapter.Open's flag-parsing
// path.
func NewWorkspace(home *Home) *Workspace {
	return &Workspace{home: home}
}

// Workspace implements tool.Workspace for one resolved Claude home. A
// Workspace is created fresh per command invocation via Adapter.Open, so
// its import-scoped fields are safe to mutate across one Stage/Finalize
// lifecycle but must not be reused across two independent import runs.
type Workspace struct {
	home *Home

	// historyAppends and configBlock accumulate cross-entry merge state for
	// one import run: Stage appends to them as it sees history.jsonl and
	// config.json entries; Finalize consumes them. Neither is plain file
	// promotion, so neither produces an archive.Staged record.
	historyAppends [][]byte
	configBlock    []byte
}

// Root implements tool.Workspace.
func (workspace *Workspace) Root() string { return workspace.home.Dir }

// LockPath implements tool.Workspace.
func (workspace *Workspace) LockPath() string {
	return filepath.Join(workspace.home.Dir, lock.FileName)
}

// ActiveWriters implements tool.Workspace.
func (workspace *Workspace) ActiveWriters() ([]tool.ActiveWriter, error) {
	return FindActive(workspace.home)
}
