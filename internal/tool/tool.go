// Package tool defines shared contracts for supported coding tools.
package tool

import "errors"

var (
	// ErrToolAbsent reports that a tool has no state on this machine.
	ErrToolAbsent = errors.New("tool has no state on this machine")
	// ErrProjectAbsent reports that a tool does not know a project.
	ErrProjectAbsent = errors.New("project unknown to this tool")
	// ErrNoWitness reports that liveness evidence could not be read.
	ErrNoWitness = errors.New("liveness evidence unavailable")
)

// Category describes a tool-local export category.
type Category struct {
	Name            string
	Description     string
	DefaultSelected bool

	// ExcludedFromAll keeps the category out of the --all sweep: it is
	// exported only via an explicit --include, a picker selection, or a
	// manifest that marks it included. For permission-grant categories,
	// whose payload widens what a tool may do on the destination machine,
	// porting stays a deliberate act.
	ExcludedFromAll bool
}

// Qualified identifies a category within a tool.
type Qualified struct {
	Tool     string
	Category string
}

// MoveRequest describes one project-path move.
type MoveRequest struct {
	OldPath     string
	NewPath     string
	RefsOnly    bool
	DeepRewrite bool
}

// ActiveWriter is liveness evidence for a running tool process.
type ActiveWriter struct {
	Pid int
	Cwd string
}
