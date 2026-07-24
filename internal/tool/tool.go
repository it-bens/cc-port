// Package tool defines shared contracts for supported coding tools.
package tool

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

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

// MCPServer is one MCP server definition a tool launches at session start,
// under the key it is registered by. At most one transport is populated: a
// stdio definition carries Command and Args, an HTTP-transport definition
// carries URL. Which one a tool's raw definition means is the adapter's
// decision, so a definition that names both never reaches a consumer as a
// hybrid of the two.
type MCPServer struct {
	Name    string
	Command string
	Args    []string
	URL     string
}

// noLaunchTarget stands in for a definition carrying neither transport. A
// blank line at a consent point would read as a server that launches nothing,
// when the truth is that cc-port found nothing to report.
const noLaunchTarget = "(no launch target)"

// LaunchLine renders what the tool would run for this definition: the stdio
// command with its arguments, the HTTP transport's endpoint, or
// noLaunchTarget when the definition names neither. A command or argument
// containing whitespace, quotes, or control characters renders strconv.Quoted;
// every other value renders verbatim.
func (server MCPServer) LaunchLine() string {
	switch {
	case server.Command != "":
		parts := make([]string, 0, len(server.Args)+1)
		for _, part := range append([]string{server.Command}, server.Args...) {
			parts = append(parts, quoteAmbiguousLaunchPart(part))
		}
		return strings.Join(parts, " ")
	case server.URL != "":
		return server.URL
	default:
		return noLaunchTarget
	}
}

// quoteAmbiguousLaunchPart returns part strconv.Quoted when rendering it
// verbatim would misrepresent the definition at a consent surface: embedded
// whitespace makes args:["a b"] read as the two arguments args:["a","b"],
// and a quote or control character forges structure the definition does not
// carry. Every other part renders verbatim.
func quoteAmbiguousLaunchPart(part string) string {
	ambiguous := strings.ContainsFunc(part, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || r == '"' || r == '\''
	})
	if !ambiguous {
		return part
	}
	return strconv.Quote(part)
}

// EscapeControl renders every control character in s — C0, DEL, and the C1
// range — as its Go escape sequence (`\x1b`, `\r`, …). Nothing is stripped:
// a consent surface shows the operator what an archive-controlled string
// actually carries instead of letting a terminal execute it.
func EscapeControl(s string) string {
	if utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			// A bare byte that is not valid UTF-8 (a 0x9b CSI, for example)
			// still acts as a control byte in a legacy terminal, so it escapes
			// instead of passing through.
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case unicode.IsControl(r):
			quoted := strconv.QuoteRune(r)
			b.WriteString(quoted[1 : len(quoted)-1])
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}
