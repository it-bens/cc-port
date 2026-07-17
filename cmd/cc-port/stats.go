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
	"github.com/it-bens/cc-port/internal/tool/claude"
)

// newStatsCmd returns the stats subcommand. With a project argument it reports
// that project's full footprint; with none it ranks every project by disk
// footprint. It is read-only: no lock, no progress wrapper, writing its result
// to cmd.OutOrStdout() like move's dry-run. The root --json persistent flag
// switches the result from the human table to the DTO.
func newStatsCmd(claudeDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "stats [<project-path>]",
		Short: "Report a project's footprint in ~/.claude",
		Long: "Reports how entangled a project's path is across shared Claude Code files\n" +
			"and how much disk its own data uses. With no argument, ranks every project\n" +
			"by disk footprint. Read-only — it never writes.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
				return &usageError{err: err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			claudeHome, err := claude.NewHome(*claudeDir)
			if err != nil {
				return err
			}
			asJSON, err := cmd.Flags().GetBool("json")
			if err != nil {
				return fmt.Errorf("read --json flag: %w", err)
			}

			if len(args) == 1 {
				return runStatsProject(cmd.Context(), cmd.OutOrStdout(), claudeHome, args[0], asJSON)
			}
			return runStatsAll(cmd.Context(), cmd.OutOrStdout(), claudeHome, asJSON)
		},
	}
}

func runStatsProject(
	ctx context.Context,
	stdout io.Writer,
	claudeHome *claude.Home,
	rawPath string,
	asJSON bool,
) error {
	projectPath, err := tool.ResolveProjectPath(rawPath)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}
	footprint, err := stats.ComputeFootprint(ctx, claudeHome, projectPath)
	if err != nil {
		return err
	}
	if asJSON {
		return writeStatsJSON(stdout, footprint)
	}
	return renderFootprint(stdout, footprint)
}

func runStatsAll(ctx context.Context, stdout io.Writer, claudeHome *claude.Home, asJSON bool) error {
	footprints, err := stats.ComputeAllFootprints(ctx, claudeHome)
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

// statsSurfaceLabels maps reference-surface keys to the file-shaped labels shown
// in the human table; keys absent from the map fall back to the key itself.
var statsSurfaceLabels = map[string]string{
	"history":                    "history.jsonl",
	"sessions":                   "sessions/*.json",
	"transcripts":                "transcripts",
	"memory":                     "memory",
	"config":                     "~/.claude.json",
	"settings":                   "settings.json",
	"plugins/installed_plugins":  "plugins/installed_plugins.json",
	"plugins/known_marketplaces": "plugins/known_marketplaces.json",
	"todos":                      "todos/",
	"usage-data/session-meta":    "usage-data/session-meta/",
	"usage-data/facets":          "usage-data/facets/",
	"plugins-data":               "plugins/data/",
	"tasks":                      "tasks/",
}

func statsSurfaceLabel(surface string) string {
	if label, ok := statsSurfaceLabels[surface]; ok {
		return label
	}
	return surface
}

func renderFootprint(stdout io.Writer, footprint *stats.Footprint) error {
	var builder strings.Builder

	fmt.Fprintf(&builder, "cc-port stats: %s\n\n", footprint.ProjectPath)
	fmt.Fprintf(&builder, "  Storage: %s\n\n", footprint.ProjectDir)

	// Transcript references reflect what a move would touch under
	// --rewrite-transcripts; a default move leaves transcripts untouched.
	fmt.Fprintf(&builder, "  References (%d occurrences)\n", footprint.ReferenceTotal)
	for _, reference := range footprint.References {
		if reference.Count == 0 {
			continue
		}
		fmt.Fprintf(&builder, "    %-32s %d\n", statsSurfaceLabel(reference.Surface), reference.Count)
	}
	fmt.Fprintln(&builder)

	fmt.Fprintf(&builder, "  Disk footprint (%d files, %s)\n", footprint.DiskFiles, humanizeBytes(footprint.DiskBytes))
	for _, usage := range footprint.Disk {
		if usage.Files == 0 {
			continue
		}
		fmt.Fprintf(&builder, "    %-16s %4d files  %s\n", usage.Category, usage.Files, humanizeBytes(usage.Bytes))
	}
	fmt.Fprintln(&builder)

	fmt.Fprintf(&builder, "  History entries: %d\n", footprint.HistoryEntryCount)
	fmt.Fprintf(&builder, "  Session files:   %d\n", footprint.SessionFileCount)

	_, err := io.WriteString(stdout, builder.String())
	return err
}

func renderAllFootprints(stdout io.Writer, footprints []stats.ProjectFootprint) error {
	var builder strings.Builder

	fmt.Fprintf(&builder, "cc-port stats: %d projects (ranked by disk footprint)\n\n", len(footprints))
	for _, footprint := range footprints {
		label := footprint.Label
		if !footprint.Resolved {
			label += " (no session witness)"
		}
		fmt.Fprintf(&builder, "  %10s  %4d files  %s\n", humanizeBytes(footprint.Bytes), footprint.Files, label)
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
