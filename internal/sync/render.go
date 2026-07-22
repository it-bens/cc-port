package sync

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Render writes the push plan summary to w. apply selects the header line:
// [dry-run] when false, the bare command line when true. The cmd layer adds
// the trailing "(no changes; pass --apply to commit)" line on dry-run and the
// "Pushed:" confirmation on apply; Render itself ends after the cross-machine
// warning (or its absence).
func (p *PushPlan) Render(w io.Writer, apply bool) error {
	var b strings.Builder

	pipelineDesc := "export -> "
	if p.EncryptionEnabled {
		pipelineDesc += "encrypt -> "
	}
	pipelineDesc += "remote sink"

	encStatus := "disabled"
	if p.EncryptionEnabled {
		encStatus = "enabled"
	}

	fmt.Fprintf(&b, "%s\n\n", planHeader("push", p.Name, apply))
	fmt.Fprintf(&b, "  Pipeline: %s\n", pipelineDesc)
	fmt.Fprintf(&b, "  Categories: %s\n", selectionSummary(p.Selected))
	fmt.Fprintf(&b, "  Encryption: %s\n\n", encStatus)

	if p.PriorPushedBy != "" {
		fmt.Fprintln(&b, "  Prior remote:")
		fmt.Fprintf(&b, "    Pushed by:   %s\n", p.PriorPushedBy)
		fmt.Fprintf(&b, "    Pushed at:   %s\n", p.PriorPushedAt.Format(time.RFC3339))
		fmt.Fprintf(&b, "    Size:        %s\n", humanizeBytes(p.PriorSize))
		fmt.Fprintf(&b, "    Encrypted:   %s\n\n", yesNo(p.PriorEncrypted))
	}

	fmt.Fprintf(&b, "  Self pusher:  %s\n", p.SelfPusher)

	if p.CrossMachine {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "  ! Cross-machine conflict: remote was last pushed from a different machine.")
		fmt.Fprintln(&b, "    Pass --force to override and overwrite, or pull first to merge manually.")
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// Render writes the pull plan summary to w. apply selects the header line:
// [dry-run] when false, the bare command line when true.
func (p *PullPlan) Render(w io.Writer, apply bool) error {
	var b strings.Builder

	pipelineDesc := "remote source -> "
	if p.RemoteEncrypted {
		pipelineDesc += "decrypt -> "
	}
	pipelineDesc += "import core"

	encStatus := "disabled"
	if p.RemoteEncrypted {
		encStatus = "enabled"
	}

	fmt.Fprintf(&b, "%s\n\n", planHeader("pull", p.Name, apply))
	fmt.Fprintf(&b, "  Pipeline: %s\n", pipelineDesc)
	fmt.Fprintf(&b, "  Encryption: %s\n\n", encStatus)

	fmt.Fprintln(&b, "  Remote:")
	fmt.Fprintf(&b, "    Pushed by:   %s\n", p.RemotePushedBy)
	fmt.Fprintf(&b, "    Pushed at:   %s\n", p.RemotePushedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "    Size:        %s\n", humanizeBytes(p.RemoteSize))
	fmt.Fprintf(&b, "    Tools:       %s\n\n", strings.Join(p.Tools, ", "))

	totalUnresolved := 0
	for _, toolName := range p.Tools {
		declared := p.DeclaredPlaceholders[toolName]
		if len(declared) == 0 {
			continue
		}
		unresolvedSet := make(map[string]bool, len(p.UnresolvedPlaceholders[toolName]))
		for _, key := range p.UnresolvedPlaceholders[toolName] {
			unresolvedSet[key] = true
		}
		fmt.Fprintf(&b, "  Required resolutions (%s):\n", toolName)
		for _, placeholder := range declared {
			if unresolvedSet[placeholder.Key] {
				fmt.Fprintf(&b, "    %s     <unresolved>        (MISSING; supply --from-manifest with <resolve> for %s)\n",
					placeholder.Key, placeholder.Key)
				totalUnresolved++
			} else {
				fmt.Fprintf(&b, "    %s     <provided>          (resolved)\n", placeholder.Key)
			}
		}
		fmt.Fprintln(&b)
	}

	if totalUnresolved > 0 {
		fmt.Fprintf(&b, "  ! %d placeholder unresolved. Pull will fail at apply time.\n", totalUnresolved)
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// planHeader renders the summary header line for a push or pull plan. A
// dry-run prefixes [dry-run]; an apply run renders the bare command line,
// because the summary then serves as the preamble to a committed operation.
func planHeader(command, name string, apply bool) string {
	if apply {
		return fmt.Sprintf("cc-port %s %s", command, name)
	}
	return fmt.Sprintf("[dry-run] cc-port %s %s", command, name)
}

func humanizeBytes(n int64) string {
	const (
		_ = 1 << (10 * iota)
		kib
		mib
		gib
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// selectionSummary renders a tool -> category selection as
// "<tool>: cat1, cat2", one clause per tool in alphabetical tool-name order
// (stable regardless of map iteration), listing every enabled category by
// name even when every category is enabled.
func selectionSummary(selected map[string]map[string]bool) string {
	if len(selected) == 0 {
		return "none"
	}
	toolNames := make([]string, 0, len(selected))
	for name := range selected {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	clauses := make([]string, 0, len(toolNames))
	for _, name := range toolNames {
		categories := selected[name]
		enabled := make([]string, 0, len(categories))
		for category, included := range categories {
			if included {
				enabled = append(enabled, category)
			}
		}
		sort.Strings(enabled)
		clauses = append(clauses, fmt.Sprintf("%s: %s", name, strings.Join(enabled, ", ")))
	}
	return strings.Join(clauses, "; ")
}
