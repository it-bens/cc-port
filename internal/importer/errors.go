// Package importer handles importing cc-port ZIP archives across every
// selected tool. Tests assert against these values via errors.Is / errors.As
// rather than substring-matching Error() output.
package importer

import (
	"errors"
	"fmt"
	"strings"
)

// ErrSourceNil is returned by Run when Options.Source is nil. The wrapping
// message hints that the caller's pipeline likely missed MaterializeStage.
var ErrSourceNil = errors.New("source is nil; pipeline missing MaterializeStage")

// ErrNoTargets is returned by Run when no tool was selected to import into.
var ErrNoTargets = errors.New("importer: no tools selected")

// UnknownEntryToolError reports an archive entry whose leading path segment
// names a tool this cc-port binary does not register at all. Distinct from
// a registered tool simply not being selected this run (which is silently
// skipped): an unrecognized name signals an archive built by a newer or
// foreign cc-port.
type UnknownEntryToolError struct {
	Tool string
	Name string
}

func (e *UnknownEntryToolError) Error() string {
	return fmt.Sprintf("archive entry %q names unregistered tool %q", e.Name, e.Tool)
}

// MissingResolutionsError reports declared placeholder keys, within one
// tool's namespace, that are present in the archive but have no resolution.
type MissingResolutionsError struct {
	Tool string
	Keys []string
}

func (e *MissingResolutionsError) Error() string {
	return fmt.Sprintf("missing resolutions for %s declared placeholder(s): %s", e.Tool, strings.Join(e.Keys, ", "))
}

// UndeclaredResolutionKeysError reports --from-manifest resolutions that are
// not keys declared by the archive's block for Tool.
type UndeclaredResolutionKeysError struct {
	Tool    string
	Keys    []string
	Surface string
}

func (e *UndeclaredResolutionKeysError) Error() string {
	return fmt.Sprintf("%s declares unknown resolution key(s) for archive tool %q: %s", e.Surface, e.Tool, strings.Join(e.Keys, ", "))
}

// ImplicitKeyOverrideError reports --from-manifest resolutions that target
// keys the import target derives as implicit anchors.
type ImplicitKeyOverrideError struct {
	Tool    string
	Keys    []string
	Surface string
}

func (e *ImplicitKeyOverrideError) Error() string {
	return fmt.Sprintf("%s overrides implicit resolution key(s) for archive tool %q: %s", e.Surface, e.Tool, strings.Join(e.Keys, ", "))
}
