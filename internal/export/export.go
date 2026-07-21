// Package export produces cc-port ZIP archives across every selected tool.
package export

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/tool"
)

// now is a seam reassigned under t.Cleanup so tests can pin timestamps.
var now = time.Now

// Options holds every parameter for one export run across the selected
// targets. Selected and Placeholders are keyed by tool name; a tool absent
// from Selected exports nothing (an empty category selection), matching
// "a tool that does not know the project writes an empty tool block."
type Options struct {
	ProjectPath  string
	Output       io.Writer
	Selected     map[string]map[string]bool
	Placeholders map[string][]manifest.Placeholder
	SyncPushedBy string
	SyncPushedAt time.Time

	// Reporter receives the export progress event stream. Defaults to
	// progress.Noop() when nil.
	Reporter progress.Reporter
}

// Result summarizes one export run: the archive-relative metadata entry
// plus every selected tool's tool.ExportResult, keyed by tool name.
type Result struct {
	Metadata archive.WrittenEntry
	ByTool   map[string]tool.ExportResult
}

// Run executes the export: for every target, discovers or reuses that
// tool's placeholders, streams its selected categories into the shared
// archive under a "<tool>/" prefix, and writes one metadata.xml at the
// archive root with one <tool> block per target. A target whose Export
// reports tool.ErrProjectAbsent contributes an empty category block rather
// than failing the run — that tool simply does not know this project.
func Run(ctx context.Context, targets []tool.Target, options *Options) (result Result, err error) {
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("canceled: %w", err)
	}
	if options.Reporter == nil {
		options.Reporter = progress.Noop()
	}
	result.ByTool = make(map[string]tool.ExportResult, len(targets))

	archiveWriter := zip.NewWriter(options.Output)
	defer func() {
		if cerr := archiveWriter.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("finalize archive: %w", cerr))
		}
	}()

	metadata := &manifest.Metadata{Created: now()}
	if options.SyncPushedBy != "" {
		metadata.SyncPushedBy = options.SyncPushedBy
	}
	if !options.SyncPushedAt.IsZero() {
		metadata.SyncPushedAt = options.SyncPushedAt.UTC().Format(time.RFC3339)
	}

	archivePhase := options.Reporter.Phase("archive", int64(len(targets)), progress.UnitItems)
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		toolPhase := archivePhase.SubPhase(target.Tool.Name(), 0, progress.UnitItems)

		selected := options.Selected[target.Tool.Name()]
		placeholders := options.Placeholders[target.Tool.Name()]
		sink := archive.NewSink(archiveWriter, target.Tool.Name(), placeholders)

		exportResult, exportErr := target.Workspace.Export(ctx, options.ProjectPath, selected, sink)
		if exportErr != nil {
			if !errors.Is(exportErr, tool.ErrProjectAbsent) {
				return result, fmt.Errorf("export %s: %w", target.Tool.Name(), exportErr)
			}
			// This tool does not know the project: write an empty block
			// (every category excluded, no placeholders) rather than
			// failing the whole run.
			exportResult = tool.ExportResult{Categories: map[string][]tool.ArchiveEntry{}}
			selected, placeholders = manifest.AbsentToolBlock()
		}
		result.ByTool[target.Tool.Name()] = exportResult
		metadata.Tools = append(metadata.Tools, manifest.Tool{
			Name:         target.Tool.Name(),
			Categories:   manifest.BuildToolCategoryEntries(categoryNames(target.Tool), selected),
			Placeholders: placeholders,
		})
		toolPhase.End("")
		archivePhase.Advance(1)
	}
	archivePhase.End("")

	written, err := archive.WriteMetadata(archiveWriter, metadata)
	if err != nil {
		return result, fmt.Errorf("write metadata.xml: %w", err)
	}
	result.Metadata = written

	return result, nil
}

func categoryNames(t tool.Tool) []string {
	categories := t.Categories()
	names := make([]string, len(categories))
	for i, category := range categories {
		names[i] = category.Name
	}
	return names
}
