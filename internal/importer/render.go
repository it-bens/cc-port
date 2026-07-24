package importer

import (
	"fmt"
	"io"
	"strings"
)

// Render writes the import plan summary to w, ending with the --apply hint.
func (p *Plan) Render(w io.Writer) error {
	var b strings.Builder

	fmt.Fprintln(&b, "cc-port import (dry-run)")
	fmt.Fprintf(&b, "  Target: %s\n\n", p.TargetPath)

	for _, toolPlan := range p.Tools {
		fmt.Fprintf(&b, "  [%s]\n", toolPlan.Name)
		fmt.Fprintf(&b, "    Categories: %s\n", strings.Join(toolPlan.Categories, ", "))
		fmt.Fprintf(&b, "    Entries:    %d\n\n", toolPlan.Entries)
	}

	if len(p.SkippedTools) > 0 {
		fmt.Fprintf(&b, "  ! archive has no data for: %s\n\n", strings.Join(p.SkippedTools, ", "))
	}

	if err := RenderMCPServers(&b, p.NewMCPServers); err != nil {
		return err
	}

	fmt.Fprintln(&b, "  Run with --apply to execute.")

	_, err := io.WriteString(w, b.String())
	return err
}

// RenderMCPServers writes the plan section naming every MCP server definition
// an apply would newly configure, each with the command line the tool would
// run for it. Nothing is written when sets is empty. This section is where an
// operator consents to the launch-at-session-start consequence of importing
// them.
func RenderMCPServers(w io.Writer, sets []MCPServerSet) error {
	if len(sets) == 0 {
		return nil
	}
	var b strings.Builder
	for _, set := range sets {
		fmt.Fprintf(&b, "  New MCP servers (%s):\n", set.Tool)
		for _, server := range set.Servers {
			fmt.Fprintf(&b, "    %-24s %s\n", server.Name, server.LaunchLine())
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b, "  ! These start automatically with every session once imported.")
	fmt.Fprintln(&b)

	_, err := io.WriteString(w, b.String())
	return err
}
