// Package move implements project directory move operations across every
// selected tool.
package move

import (
	"context"
	"errors"
	"fmt"

	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/rewrite"
	"github.com/it-bens/cc-port/internal/tool"
)

// Options holds the parameters for a project move operation.
type Options struct {
	OldPath     string
	NewPath     string
	RefsOnly    bool
	DeepRewrite bool

	// Reporter receives the progress event stream during Apply. Defaults
	// to progress.Noop() when nil. DryRun does not use it.
	Reporter progress.Reporter
}

func (options Options) request() tool.MoveRequest {
	return tool.MoveRequest{
		OldPath:     options.OldPath,
		NewPath:     options.NewPath,
		RefsOnly:    options.RefsOnly,
		DeepRewrite: options.DeepRewrite,
	}
}

// SurfaceCount is one surface's replacement count.
type SurfaceCount struct {
	Name  string
	Count int
}

// ToolPlan is one target's dry-run contribution. Absent is true when the
// target reported tool.ErrProjectAbsent: it simply does not know this
// project, and Surfaces is empty rather than fabricated.
type ToolPlan struct {
	Tool     string
	Absent   bool
	Surfaces []SurfaceCount
	Warnings []string
}

// Plan holds the results of a dry-run move operation across every target.
type Plan struct {
	ByTool   []ToolPlan
	Warnings []string
}

// NoPhysicalMoveWarning reports a selected tool set that only rewrites state
// references and does not relocate the project directory on disk.
const NoPhysicalMoveWarning = "no selected tool moves the project directory on disk"

// ErrNestedMove is returned when a move's destination equals its source, or
// is a path-boundary descendant of it. Either case would have a later
// surface's rewrite race its own output (an adapter mid-rewriting OldPath
// references while NewPath — still nested inside OldPath — is itself part of
// the tree being rewritten), corrupting state. Checked once, for every tool
// and every mode (--apply, dry-run, --refs-only), before any adapter surface
// runs.
var ErrNestedMove = errors.New("refusing to move: new path equals or is nested inside old path")

func validateNotNested(oldPath, newPath string) error {
	if oldPath == newPath {
		return fmt.Errorf("%w: %q", ErrNestedMove, newPath)
	}
	if rewrite.IsBoundaryDescendant(oldPath, newPath) {
		return fmt.Errorf("%w: %q is nested inside %q", ErrNestedMove, newPath, oldPath)
	}
	return nil
}

// DryRun computes the move plan without writing any files; lock-free
// contrast to Apply.
func DryRun(ctx context.Context, targets []tool.Target, options Options) (*Plan, error) {
	if err := validateNotNested(options.OldPath, options.NewPath); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("canceled: %w", err)
	}
	req := options.request()

	plan := &Plan{}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		toolPlan, err := planTarget(ctx, target, req)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", target.Tool.Name(), err)
		}
		plan.ByTool = append(plan.ByTool, toolPlan)
	}
	if !options.RefsOnly && !planContainsProjectDirectory(plan) {
		plan.Warnings = append(plan.Warnings, NoPhysicalMoveWarning)
	}
	return plan, nil
}

func planContainsProjectDirectory(plan *Plan) bool {
	for _, toolPlan := range plan.ByTool {
		if toolPlan.Absent {
			continue
		}
		for _, surface := range toolPlan.Surfaces {
			if surface.Name == tool.SurfaceProjectDirectory {
				return true
			}
		}
	}
	return false
}

func planTarget(ctx context.Context, target tool.Target, req tool.MoveRequest) (ToolPlan, error) {
	toolPlan := ToolPlan{Tool: target.Tool.Name()}

	surfaces, err := target.Workspace.MoveSurfaces(req)
	if err != nil {
		if errors.Is(err, tool.ErrProjectAbsent) {
			toolPlan.Absent = true
			return toolPlan, nil
		}
		return ToolPlan{}, err
	}
	for _, surface := range surfaces {
		count, err := surface.Plan(ctx)
		if err != nil {
			return ToolPlan{}, fmt.Errorf("plan surface %s: %w", surface.Name, err)
		}
		toolPlan.Surfaces = append(toolPlan.Surfaces, SurfaceCount{Name: surface.Name, Count: count})
	}

	warnings, err := target.Workspace.ResidualWarnings(req)
	if err != nil {
		return ToolPlan{}, fmt.Errorf("residual warnings: %w", err)
	}
	toolPlan.Warnings = warnings
	return toolPlan, nil
}

// ToolResult is one target's apply outcome.
type ToolResult struct {
	Tool     string
	Absent   bool
	Success  bool
	Err      error
	Surfaces []SurfaceCount
	Warnings []string
}

// ApplyResult is the per-tool outcome of an Apply run.
type ApplyResult struct {
	ByTool   []ToolResult
	Warnings []string
}

// Failed reports whether any target failed.
func (result *ApplyResult) Failed() bool {
	for _, toolResult := range result.ByTool {
		if !toolResult.Success && !toolResult.Absent {
			return true
		}
	}
	return false
}

// Apply performs the project move. Every selected target is preflighted in
// registry order (MoveSurfaces, writer witness, then flock) before any tool
// applies. The flocks remain held until the full apply completes. Cross-tool
// rollback does not exist: a target that has
// already completed reflects the true new path even if a later target
// fails, so the returned ApplyResult carries a per-tool success/failure
// record and Failed reports whether the caller should exit non-zero.
func Apply(ctx context.Context, targets []tool.Target, options Options) (result *ApplyResult, returnErr error) {
	if err := validateNotNested(options.OldPath, options.NewPath); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("canceled: %w", err)
	}
	if options.Reporter == nil {
		options.Reporter = progress.Noop()
	}
	req := options.request()

	type prepared struct {
		target   tool.Target
		surfaces []tool.Surface
		held     *lock.Held
		absent   bool
	}
	var preparedTargets []prepared
	result = &ApplyResult{}
	defer func() {
		var releaseErrors []error
		for index := len(preparedTargets) - 1; index >= 0; index-- {
			if err := preparedTargets[index].held.Release(); err != nil {
				releaseErrors = append(releaseErrors, fmt.Errorf("release %s lock: %w", preparedTargets[index].target.Tool.Name(), err))
			}
		}
		if len(releaseErrors) > 0 {
			returnErr = errors.Join(returnErr, errors.Join(releaseErrors...))
		}
	}()

	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		surfaces, err := target.Workspace.MoveSurfaces(req)
		absent := false
		if err != nil {
			if errors.Is(err, tool.ErrProjectAbsent) {
				absent = true
				surfaces = nil
			} else {
				return nil, fmt.Errorf("preflight %s: %w", target.Tool.Name(), err)
			}
		}
		held, err := lock.Acquire(target.Workspace.LockPath(), target.Workspace.ActiveWriters)
		if err != nil {
			return nil, fmt.Errorf("preflight %s: %w", target.Tool.Name(), err)
		}
		preparedTargets = append(preparedTargets, prepared{target: target, surfaces: surfaces, held: held, absent: absent})
	}

	for _, entry := range preparedTargets {
		if entry.absent {
			result.ByTool = append(result.ByTool, ToolResult{Tool: entry.target.Tool.Name(), Absent: true})
			continue
		}
		toolResult := applyTarget(ctx, entry.target, entry.surfaces, req, options.Reporter)
		result.ByTool = append(result.ByTool, toolResult)
	}
	if !options.RefsOnly {
		hasProjectDirectory := false
		for _, entry := range preparedTargets {
			if entry.absent {
				continue
			}
			for _, surface := range entry.surfaces {
				if surface.Name == tool.SurfaceProjectDirectory {
					hasProjectDirectory = true
					break
				}
			}
		}
		if !hasProjectDirectory {
			result.Warnings = append(result.Warnings, NoPhysicalMoveWarning)
		}
	}

	return result, nil
}

func applyTarget(ctx context.Context, target tool.Target, surfaces []tool.Surface, req tool.MoveRequest, reporter progress.Reporter) ToolResult {
	toolResult := ToolResult{Tool: target.Tool.Name()}
	phase := reporter.Phase(target.Tool.Name(), int64(len(surfaces)), progress.UnitItems)
	_, preApplyWarningErr := target.Workspace.ResidualWarnings(req)
	if preApplyWarningErr != nil {
		toolResult.Warnings = append(toolResult.Warnings, fmt.Sprintf("could not inspect residual warnings: %v", preApplyWarningErr))
	}

	undo := tool.NewRestorer()
	var err error
	for _, surface := range surfaces {
		if err = ctx.Err(); err != nil {
			restoreErr := undo.Restore()
			err = errors.Join(fmt.Errorf("apply canceled: %w", err), restoreErr)
			break
		}
		count, applyErr := surface.Apply(ctx, undo)
		if applyErr != nil {
			restoreErr := undo.Restore()
			err = errors.Join(fmt.Errorf("apply surface %s: %w", surface.Name, applyErr), restoreErr)
			break
		}
		toolResult.Surfaces = append(toolResult.Surfaces, SurfaceCount{Name: surface.Name, Count: count})
		phase.Advance(1)
	}
	if err == nil {
		undo.Cleanup()
	}
	phase.End("")

	postApplyWarnings, postApplyWarningErr := target.Workspace.ResidualWarnings(req)
	// Post-apply inspection is authoritative: Apply can remove pre-existing
	// findings and Codex records checkpoint warnings while it runs.
	toolResult.Warnings = appendUniqueWarnings(toolResult.Warnings, postApplyWarnings)
	if postApplyWarningErr != nil {
		inspectionWarning := fmt.Sprintf("could not inspect residual warnings: %v", postApplyWarningErr)
		toolResult.Warnings = appendUniqueWarnings(toolResult.Warnings, []string{inspectionWarning})
	}
	if err != nil {
		toolResult.Err = err
		return toolResult
	}
	toolResult.Success = true
	return toolResult
}

func appendUniqueWarnings(existing, candidates []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(candidates))
	for _, warning := range existing {
		seen[warning] = struct{}{}
	}
	for _, warning := range candidates {
		if _, exists := seen[warning]; exists {
			continue
		}
		existing = append(existing, warning)
		seen[warning] = struct{}{}
	}
	return existing
}
