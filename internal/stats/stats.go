// Package stats computes a project's footprint across every selected tool:
// how many times its path is referenced across shared files, and how much
// disk its owned data uses. It is read-only and lock-free, driven entirely
// by the tool.Auditor contract.
package stats

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/it-bens/cc-port/internal/tool"
)

// ToolFootprint is one tool's contribution to a project's footprint.
// Absent is true when the tool reported tool.ErrProjectAbsent: it simply
// does not know this project, and every other field is left zero rather
// than fabricated.
type ToolFootprint struct {
	Tool           string
	Absent         bool
	References     []tool.CountSurface
	ReferenceTotal int
	Disk           []tool.SizeCategory
	DiskFiles      int
	DiskBytes      int64
}

// Footprint is a single project's full footprint, one ToolFootprint per
// selected target, in registration order.
type Footprint struct {
	ProjectPath string
	ByTool      []ToolFootprint
}

// ComputeFootprint reports the full footprint of a single project across
// every target. A target reporting tool.ErrProjectAbsent contributes a
// zero ToolFootprint (Absent: true) rather than failing the whole call.
func ComputeFootprint(ctx context.Context, targets []tool.Target, projectPath string) (*Footprint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	footprint := &Footprint{ProjectPath: projectPath}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		toolFootprint, err := computeToolFootprint(ctx, target, projectPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", target.Tool.Name(), err)
		}
		footprint.ByTool = append(footprint.ByTool, toolFootprint)
	}
	return footprint, nil
}

func computeToolFootprint(ctx context.Context, target tool.Target, projectPath string) (ToolFootprint, error) {
	result := ToolFootprint{Tool: target.Tool.Name()}

	references, err := target.Workspace.ReferenceSurfaces(ctx, projectPath)
	if err != nil {
		if errors.Is(err, tool.ErrProjectAbsent) {
			result.Absent = true
			return result, nil
		}
		return ToolFootprint{}, fmt.Errorf("reference surfaces: %w", err)
	}
	result.References = references
	for _, surface := range references {
		result.ReferenceTotal += surface.Count
	}

	disk, err := target.Workspace.DiskCategories(ctx, projectPath)
	if err != nil {
		if errors.Is(err, tool.ErrProjectAbsent) {
			return ToolFootprint{Tool: target.Tool.Name(), Absent: true}, nil
		}
		return ToolFootprint{}, fmt.Errorf("disk categories: %w", err)
	}
	result.Disk = disk
	for _, category := range disk {
		result.DiskFiles += category.Files
		result.DiskBytes += category.Bytes
	}
	return result, nil
}

// ProjectFootprint is one project one tool knows about, for all-projects
// enumeration.
type ProjectFootprint struct {
	Tool string
	tool.ProjectInfo
}

// ComputeAllFootprints reports every target's known projects, flattened into
// one list and ranked by total bytes descending across every tool combined
// (ties broken by label).
func ComputeAllFootprints(ctx context.Context, targets []tool.Target) ([]ProjectFootprint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var footprints []ProjectFootprint
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		infos, err := target.Workspace.EnumerateProjects(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: enumerate projects: %w", target.Tool.Name(), err)
		}
		for _, info := range infos {
			footprints = append(footprints, ProjectFootprint{Tool: target.Tool.Name(), ProjectInfo: info})
		}
	}

	sort.SliceStable(footprints, func(first, second int) bool {
		if footprints[first].Bytes != footprints[second].Bytes {
			return footprints[first].Bytes > footprints[second].Bytes
		}
		return footprints[first].Label < footprints[second].Label
	})
	return footprints, nil
}
