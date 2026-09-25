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
// does not know this project, and every count and size is left zero rather
// than fabricated. Warnings carries the tool's Auditor.AuditWarnings either
// way.
type ToolFootprint struct {
	Tool           string
	Absent         bool
	References     []tool.CountSurface
	ReferenceTotal int
	Disk           []tool.SizeCategory
	DiskFiles      int
	DiskBytes      int64
	Warnings       []string
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
	warnings, err := target.Workspace.AuditWarnings(ctx)
	if err != nil {
		return ToolFootprint{}, fmt.Errorf("audit warnings: %w", err)
	}
	result := ToolFootprint{Tool: target.Tool.Name(), Warnings: warnings}

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
			result.Absent = true
			return result, nil
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

// AllFootprints is the all-projects ranking plus each target's
// Auditor.AuditWarnings, keyed by tool name. A target with no warnings has
// no key.
type AllFootprints struct {
	Projects []ProjectFootprint
	Warnings map[string][]string
}

// ComputeAllFootprints reports every target's known projects, flattened into
// one list and ranked by total bytes descending across every tool combined
// (ties broken by label), with every target's audit warnings.
func ComputeAllFootprints(ctx context.Context, targets []tool.Target) (*AllFootprints, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	all := &AllFootprints{Warnings: make(map[string][]string)}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		warnings, err := target.Workspace.AuditWarnings(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: audit warnings: %w", target.Tool.Name(), err)
		}
		if len(warnings) > 0 {
			all.Warnings[target.Tool.Name()] = warnings
		}
		infos, err := target.Workspace.EnumerateProjects(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: enumerate projects: %w", target.Tool.Name(), err)
		}
		for _, info := range infos {
			all.Projects = append(all.Projects, ProjectFootprint{Tool: target.Tool.Name(), ProjectInfo: info})
		}
	}

	sort.SliceStable(all.Projects, func(first, second int) bool {
		if all.Projects[first].Bytes != all.Projects[second].Bytes {
			return all.Projects[first].Bytes > all.Projects[second].Bytes
		}
		return all.Projects[first].Label < all.Projects[second].Label
	})
	return all, nil
}
