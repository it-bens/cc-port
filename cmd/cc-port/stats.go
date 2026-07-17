package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/it-bens/cc-port/internal/stats"
	"github.com/it-bens/cc-port/internal/tool"
)

// newStatsCmd returns the stats subcommand. With a project argument it
// reports that project's full footprint per tool; with none it ranks
// every target's known projects by disk footprint. It is read-only: no
// lock, no progress wrapper. The root --json persistent flag switches the
// result from the human table to the DTO.
func newStatsCmd(toolSet *tool.Set, flags *toolFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "stats [<project-path>]",
		Short: "Report a project's footprint across every selected tool",
		Long: "Reports how entangled a project's path is across shared files for every\n" +
			"selected tool and how much disk its own data uses. With no argument, ranks\n" +
			"every known project by disk footprint. Read-only — it never writes.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := resolveTargets(toolSet, flags)
			if err != nil {
				return err
			}
			asJSON, err := cmd.Flags().GetBool("json")
			if err != nil {
				return fmt.Errorf("read --json flag: %w", err)
			}

			if len(args) == 1 {
				return runStatsProject(cmd.Context(), cmd.OutOrStdout(), targets, args[0], asJSON)
			}
			return runStatsAll(cmd.Context(), cmd.OutOrStdout(), targets, asJSON)
		},
	}
}

func runStatsProject(
	ctx context.Context, stdout io.Writer, targets []tool.Target, rawPath string, asJSON bool,
) error {
	projectPath, err := tool.ResolveProjectPath(rawPath)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}
	footprint, err := stats.ComputeFootprint(ctx, targets, projectPath)
	if err != nil {
		return err
	}
	if asJSON {
		return writeStatsJSON(stdout, footprint)
	}
	return renderFootprint(stdout, footprint)
}

func runStatsAll(ctx context.Context, stdout io.Writer, targets []tool.Target, asJSON bool) error {
	footprints, err := stats.ComputeAllFootprints(ctx, targets)
	if err != nil {
		return err
	}
	if asJSON {
		return writeStatsJSON(stdout, footprints)
	}
	return renderAllFootprints(stdout, footprints)
}

// writeStatsJSON emits the DTO as indented JSON with HTML escaping off so paths
// render literally rather than as <-style escapes.
func writeStatsJSON(stdout io.Writer, payload any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		return fmt.Errorf("encode stats JSON: %w", err)
	}
	return nil
}

func renderFootprint(stdout io.Writer, footprint *stats.Footprint) error {
	var builder strings.Builder

	fmt.Fprintf(&builder, "cc-port stats: %s\n\n", footprint.ProjectPath)

	for _, toolFootprint := range footprint.ByTool {
		fmt.Fprintf(&builder, "  [%s]\n", toolFootprint.Tool)
		if toolFootprint.Absent {
			fmt.Fprintln(&builder, "    (project unknown to this tool)")
			continue
		}

		fmt.Fprintf(&builder, "    References (%d occurrences)\n", toolFootprint.ReferenceTotal)
		for _, reference := range toolFootprint.References {
			if reference.Count == 0 {
				continue
			}
			fmt.Fprintf(&builder, "      %-24s %d\n", reference.Name, reference.Count)
		}
		fmt.Fprintln(&builder)

		fmt.Fprintf(&builder, "    Disk footprint (%d files, %s)\n",
			toolFootprint.DiskFiles, humanizeBytes(toolFootprint.DiskBytes))
		for _, usage := range toolFootprint.Disk {
			if usage.Files == 0 {
				continue
			}
			fmt.Fprintf(&builder, "      %-16s %4d files  %s\n", usage.Name, usage.Files, humanizeBytes(usage.Bytes))
		}
		fmt.Fprintln(&builder)
	}

	_, err := io.WriteString(stdout, builder.String())
	return err
}

func renderAllFootprints(stdout io.Writer, footprints []stats.ProjectFootprint) error {
	var builder strings.Builder

	fmt.Fprintf(&builder, "cc-port stats: %d known projects (ranked by disk footprint)\n\n", len(footprints))
	for _, footprint := range footprints {
		label := footprint.Label
		if !footprint.Resolved {
			label += " (no session witness)"
		}
		fmt.Fprintf(&builder, "  [%-8s] %10s  %4d files  %s\n",
			footprint.Tool, humanizeBytes(footprint.Bytes), footprint.Files, label)
	}

	_, err := io.WriteString(stdout, builder.String())
	return err
}

// humanizeBytes renders a byte count as a human-readable size. The stats table
// is the cmd layer's first byte-sized output; the sync renderer keeps its own
// copy rather than the two packages sharing a util grab-bag.
func humanizeBytes(byteCount int64) string {
	const (
		_ = 1 << (10 * iota)
		kib
		mib
		gib
	)
	switch {
	case byteCount >= gib:
		return fmt.Sprintf("%.1f GiB", float64(byteCount)/float64(gib))
	case byteCount >= mib:
		return fmt.Sprintf("%.1f MiB", float64(byteCount)/float64(mib))
	case byteCount >= kib:
		return fmt.Sprintf("%.1f KiB", float64(byteCount)/float64(kib))
	default:
		return fmt.Sprintf("%d B", byteCount)
	}
}
