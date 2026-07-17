package tool

import (
	"fmt"
	"strings"
)

// Target pairs one registered Tool with a Workspace already opened for it
// (via Tool.Open), so command packages can carry both through one call
// without re-deriving the Workspace from the Tool.
type Target struct {
	Tool      Tool
	Workspace Workspace
}

// Set is the registered collection of supported tools. NewSet validates the
// registry once at construction; every accessor thereafter iterates in
// registration order.
type Set struct {
	tools []Tool
}

// NewSet validates tools for unique names, unique qualified categories
// (a (tool, category) pair appearing twice), and unique placeholder keys
// across tools (two tools declaring the same implicit anchor key would make
// resolution ambiguous), then returns a Set iterating in registration order.
// Panics on a violated invariant: these are registry-construction bugs in
// cmd/cc-port/tools.go, not operational errors a caller can recover from.
func NewSet(tools ...Tool) *Set {
	seenNames := make(map[string]struct{}, len(tools))
	seenQualified := make(map[Qualified]struct{})
	seenPlaceholderKeys := make(map[string]string) // key -> owning tool name

	for _, t := range tools {
		name := t.Name()
		if name == "" {
			panic("tool.NewSet: a tool registered with an empty Name()")
		}
		if _, dup := seenNames[name]; dup {
			panic(fmt.Sprintf("tool.NewSet: duplicate tool name %q", name))
		}
		seenNames[name] = struct{}{}

		for _, category := range t.Categories() {
			qualified := Qualified{Tool: name, Category: category.Name}
			if _, dup := seenQualified[qualified]; dup {
				panic(fmt.Sprintf("tool.NewSet: duplicate qualified category %s/%s", name, category.Name))
			}
			seenQualified[qualified] = struct{}{}
		}

		for _, key := range t.ImplicitAnchorKeys() {
			if owner, dup := seenPlaceholderKeys[key]; dup {
				panic(fmt.Sprintf("tool.NewSet: placeholder key %s claimed by both %q and %q", key, owner, name))
			}
			seenPlaceholderKeys[key] = name
		}
	}

	return &Set{tools: tools}
}

// All returns every registered tool in registration order.
func (set *Set) All() []Tool {
	return set.tools
}

// ByName returns the tool registered under name, or ok=false when no tool
// matches.
func (set *Set) ByName(name string) (Tool, bool) {
	for _, t := range set.tools {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

// Detected returns every registered tool whose Detect() reports true, in
// registration order. A Detect error aborts and is surfaced to the caller
// with the offending tool named.
func (set *Set) Detected() ([]Tool, error) {
	var detected []Tool
	for _, t := range set.tools {
		ok, err := t.Detect()
		if err != nil {
			return nil, fmt.Errorf("detect %s: %w", t.Name(), err)
		}
		if ok {
			detected = append(detected, t)
		}
	}
	return detected, nil
}

// ParseQualified parses a "<tool>/<category>" argument (the --include
// flag's grammar) into a Qualified value. A bare category name with no
// slash is rejected: multi-tool selection requires the tool segment.
func ParseQualified(raw string) (Qualified, error) {
	index := strings.IndexByte(raw, '/')
	if index <= 0 || index == len(raw)-1 {
		return Qualified{}, fmt.Errorf(
			"invalid --include value %q: expected \"<tool>/<category>\" (bare category names are not accepted)", raw,
		)
	}
	return Qualified{Tool: raw[:index], Category: raw[index+1:]}, nil
}
