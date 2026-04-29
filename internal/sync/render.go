package sync

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/it-bens/cc-port/internal/manifest"
)

// Render writes the dry-run preview for a push plan to w. The cmd
// layer adds the trailing "(no changes; pass --apply to commit)" line
// when --apply is not set; Render itself ends after the
// cross-machine warning (or its absence).
func (p *PushPlan) Render(w io.Writer) error {
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

	fmt.Fprintf(&b, "[dry-run] cc-port push %s\n\n", p.Name)
	fmt.Fprintf(&b, "  Pipeline: %s\n", pipelineDesc)
	fmt.Fprintf(&b, "  Categories: %s\n", categoriesSummary(p.Categories))
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

// Render writes the dry-run preview for a pull plan to w.
func (p *PullPlan) Render(w io.Writer) error {
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

	fmt.Fprintf(&b, "[dry-run] cc-port pull %s\n\n", p.Name)
	fmt.Fprintf(&b, "  Pipeline: %s\n", pipelineDesc)
	fmt.Fprintf(&b, "  Encryption: %s\n\n", encStatus)

	fmt.Fprintln(&b, "  Remote:")
	fmt.Fprintf(&b, "    Pushed by:   %s\n", p.RemotePushedBy)
	fmt.Fprintf(&b, "    Pushed at:   %s\n", p.RemotePushedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "    Size:        %s\n", humanizeBytes(p.RemoteSize))
	fmt.Fprintf(&b, "    Categories:  %s\n\n", categoriesSummary(p.Categories))

	if len(p.DeclaredPlaceholders) > 0 {
		fmt.Fprintln(&b, "  Required resolutions:")
		unresolvedSet := make(map[string]bool, len(p.UnresolvedPlaceholders))
		for _, key := range p.UnresolvedPlaceholders {
			unresolvedSet[key] = true
		}
		for _, placeholder := range p.DeclaredPlaceholders {
			if unresolvedSet[placeholder.Key] {
				fmt.Fprintf(&b, "    %s     <unresolved>        (MISSING; pass --resolution %s=...)\n",
					placeholder.Key, strings.Trim(placeholder.Key, "{}"))
			} else {
				fmt.Fprintf(&b, "    %s     <provided>          (resolved)\n", placeholder.Key)
			}
		}
		fmt.Fprintln(&b)
	}

	if len(p.UnresolvedPlaceholders) > 0 {
		fmt.Fprintf(&b, "  ! %d placeholder unresolved. Pull will fail at apply time.\n",
			len(p.UnresolvedPlaceholders))
	}

	_, err := io.WriteString(w, b.String())
	return err
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

// categoriesSummary returns "all" if every known category is set,
// else a comma-separated list of enabled category names in
// AllCategories order.
func categoriesSummary(set manifest.CategorySet) string {
	enabled := make([]string, 0, len(manifest.AllCategories))
	allSet := true
	for _, spec := range manifest.AllCategories {
		if spec.Value(&set) {
			enabled = append(enabled, spec.Name)
		} else {
			allSet = false
		}
	}
	if allSet {
		return "all"
	}
	return strings.Join(enabled, ", ")
}
