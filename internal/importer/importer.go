package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/tool"
)

// Options configures an import operation. Source/Size let the importer
// accept any random-access bytes (file, decrypted tempfile, in-memory)
// without owning archive lifecycle: callers open the source, hand it to
// Run, and close it after Run returns.
type Options struct {
	Source     io.ReaderAt
	Size       int64
	TargetPath string

	// FromManifest optionally supplies placeholder Resolve overrides per
	// tool, read from a --from-manifest file. nil means no override.
	FromManifest *manifest.Metadata

	// Reporter receives the import progress event stream. Defaults to
	// progress.Noop() when nil.
	Reporter progress.Reporter
}

// Result summarizes the observable outcome of a successful import.
type Result struct {
	// SkippedTools lists tools that were selected for this run but for
	// which the archive's manifest carried no <tool> block: the archive
	// simply has no data for them.
	SkippedTools []string

	// Warnings contains non-fatal finalize notices keyed by tool wire name.
	Warnings map[string][]string
}

// Run imports a cc-port archive into every target's Workspace. allTools is
// the full registry, used to distinguish an unregistered archive-entry tool
// prefix (hard failure) from a registered tool this run did not select
// (silently skipped). targets is the narrowed, already-opened set this run
// imports into, in registration order; every target's flock is acquired
// (nested, registry order) before any archive byte is read, and every
// target's Finalize runs, still under lock, after all tools' staged files
// have promoted as one all-or-nothing batch.
func Run(ctx context.Context, allTools *tool.Set, targets []tool.Target, options *Options) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("canceled: %w", err)
	}
	if options.Source == nil {
		return nil, fmt.Errorf("importer: %w", ErrSourceNil)
	}
	if len(targets) == 0 {
		return nil, ErrNoTargets
	}
	if options.Reporter == nil {
		options.Reporter = progress.Noop()
	}

	var result *Result
	err := withAllLocks(targets, func() error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("canceled: %w", err)
		}
		runResult, err := runLocked(ctx, allTools, targets, options)
		if err != nil {
			return err
		}
		result = runResult
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// withAllLocks acquires every target's advisory lock, innermost call last,
// so the effective acquisition order matches registry (targets) order.
func withAllLocks(targets []tool.Target, fn func() error) error {
	if len(targets) == 0 {
		return fn()
	}
	first := targets[0]
	return lock.WithLock(first.Workspace.LockPath(), first.Workspace.ActiveWriters, func() error {
		return withAllLocks(targets[1:], fn)
	})
}

func runLocked(ctx context.Context, allTools *tool.Set, targets []tool.Target, options *Options) (*Result, error) {
	preflightPhase := options.Reporter.Phase("preflight", 0, progress.UnitItems)

	metadata, err := manifest.ReadManifestFromZip(options.Source, options.Size)
	if err != nil {
		return nil, fmt.Errorf("read metadata from archive: %w", err)
	}
	if err := verifyManifestTools(allTools, metadata); err != nil {
		return nil, err
	}
	blocksByTool := make(map[string]manifest.Tool, len(metadata.Tools))
	for _, block := range metadata.Tools {
		blocksByTool[block.Name] = block
	}

	reader, err := archive.OpenReader(options.Source, options.Size)
	if err != nil {
		return nil, err
	}
	entries, err := reader.RawEntries()
	if err != nil {
		return nil, err
	}
	if err := verifyEntryTools(allTools, entries); err != nil {
		return nil, err
	}
	entriesByTool := groupEntriesByTool(entries)

	result, present, resolutionsByTool, err := preflightTargets(targets, options, blocksByTool, entriesByTool)
	if err != nil {
		return nil, err
	}

	if err := preflightStagingDirs(present, options.TargetPath); err != nil {
		return nil, err
	}
	preflightPhase.End("")

	extractPhase := options.Reporter.Phase("extract", int64(len(entries)), progress.UnitEntries)
	stagedSet, err := stageEntries(ctx, present, entriesByTool, options.TargetPath, resolutionsByTool, extractPhase)
	if err != nil {
		return nil, err
	}
	extractPhase.End("")

	promotePhase := options.Reporter.Phase("promote", int64(len(stagedSet.All())), progress.UnitItems)
	if err := promoteStaged(stagedSet); err != nil {
		return nil, err
	}
	promotePhase.End("")

	finalizePhase := options.Reporter.Phase("finalize", int64(len(present)), progress.UnitItems)
	for _, target := range present {
		warnings, err := target.Workspace.Finalize(ctx, options.TargetPath, stagedSet)
		if err != nil {
			return nil, fmt.Errorf("finalize %s: %w", target.Tool.Name(), err)
		}
		if len(warnings) > 0 {
			result.Warnings[target.Tool.Name()] = append(result.Warnings[target.Tool.Name()], warnings...)
		}
		finalizePhase.Advance(1)
	}
	finalizePhase.End("")

	return &result, nil
}

func preflightTargets(
	targets []tool.Target,
	options *Options,
	blocksByTool map[string]manifest.Tool,
	entriesByTool map[string][]archive.RawEntry,
) (Result, []tool.Target, map[string]map[string]string, error) {
	result := Result{Warnings: make(map[string][]string)}
	present := make([]tool.Target, 0, len(targets))
	resolutionsByTool := make(map[string]map[string]string, len(targets))

	for _, target := range targets {
		name := target.Tool.Name()
		block, ok := blocksByTool[name]
		if !ok {
			result.SkippedTools = append(result.SkippedTools, name)
			continue
		}
		if _, err := manifest.ApplyToolCategories(name, categoryNames(target.Tool), block.Categories); err != nil {
			return Result{}, nil, nil, fmt.Errorf("manifest categories for %s: %w", name, err)
		}
		anchors, err := target.Workspace.ImplicitAnchors(options.TargetPath)
		if err != nil {
			return Result{}, nil, nil, fmt.Errorf("implicit anchors for %s: %w", name, err)
		}
		resolutions, err := mergeResolutions(block, options.FromManifest, anchors)
		if err != nil {
			return Result{}, nil, nil, err
		}
		if err := checkMissingResolutions(name, block, anchors, resolutions, entriesByTool[name]); err != nil {
			return Result{}, nil, nil, err
		}
		resolutionsByTool[name] = resolutions
		present = append(present, target)
	}

	return result, present, resolutionsByTool, nil
}

// verifyManifestTools hard-fails when the manifest declares a <tool> block
// this binary does not register at all.
func verifyManifestTools(allTools *tool.Set, metadata *manifest.Metadata) error {
	for _, block := range metadata.Tools {
		if _, ok := allTools.ByName(block.Name); !ok {
			return fmt.Errorf("archive manifest: %w", &manifest.UnregisteredToolError{Tool: block.Name})
		}
	}
	return nil
}

// verifyEntryTools hard-fails when any archive entry's leading path segment
// names a tool this binary does not register at all.
func verifyEntryTools(allTools *tool.Set, entries []archive.RawEntry) error {
	for _, raw := range entries {
		if _, ok := allTools.ByName(raw.ToolName); !ok {
			return &UnknownEntryToolError{Tool: raw.ToolName, Name: raw.Entry.Name}
		}
	}
	return nil
}

func groupEntriesByTool(entries []archive.RawEntry) map[string][]archive.RawEntry {
	grouped := make(map[string][]archive.RawEntry)
	for _, raw := range entries {
		grouped[raw.ToolName] = append(grouped[raw.ToolName], raw)
	}
	return grouped
}

func categoryNames(t tool.Tool) []string {
	categories := t.Categories()
	names := make([]string, len(categories))
	for i, category := range categories {
		names[i] = category.Name
	}
	return names
}

// mergeResolutions merges, for one tool, the sender's own pre-filled
// Resolve values (weakest), any --from-manifest override for that tool
// (stronger), and the target's implicit anchors (strongest — cc-port
// computes these itself and a stale or malicious sender value must never
// override them).
func mergeResolutions(
	block manifest.Tool, fromManifest *manifest.Metadata, anchors map[string]string,
) (map[string]string, error) {
	resolutions := make(map[string]string)
	declared := make(map[string]struct{}, len(block.Placeholders))
	for _, placeholder := range block.Placeholders {
		declared[placeholder.Key] = struct{}{}
		if placeholder.Resolve != "" {
			resolutions[placeholder.Key] = placeholder.Resolve
		}
	}
	if fromManifest != nil {
		if overrideBlock, ok := fromManifest.ToolBlock(block.Name); ok {
			var unknown []string
			for _, placeholder := range overrideBlock.Placeholders {
				if placeholder.Resolve == "" {
					continue
				}
				if _, implicit := anchors[placeholder.Key]; implicit {
					continue
				}
				if _, ok := declared[placeholder.Key]; !ok {
					unknown = append(unknown, placeholder.Key)
					continue
				}
				resolutions[placeholder.Key] = placeholder.Resolve
			}
			if len(unknown) > 0 {
				sort.Strings(unknown)
				return nil, &UndeclaredResolutionKeysError{Tool: block.Name, Keys: unknown, Surface: "--from-manifest"}
			}
		}
	}
	for key, value := range anchors {
		if !filepath.IsAbs(value) {
			return nil, fmt.Errorf("implicit anchor %q is not absolute: %q", key, value)
		}
		resolutions[key] = value
	}
	nonImplicit := make(map[string]string, len(resolutions))
	for key, value := range resolutions {
		if _, implicit := anchors[key]; !implicit {
			nonImplicit[key] = value
		}
	}
	if err := archive.ValidateResolutions(nonImplicit); err != nil {
		return nil, fmt.Errorf("invalid resolutions for %s: %w", block.Name, err)
	}
	return resolutions, nil
}

// checkMissingResolutions refuses the import if any declared placeholder
// key that is actually referenced in one of this tool's archive bodies
// lacks a resolution, before any write has occurred. A declared key the
// archive never embeds does not need a resolution.
func checkMissingResolutions(
	toolName string, block manifest.Tool, anchors, resolutions map[string]string, entries []archive.RawEntry,
) error {
	candidateKeys := make([]string, 0, len(block.Placeholders))
	for _, placeholder := range block.Placeholders {
		if _, implicit := anchors[placeholder.Key]; implicit {
			continue
		}
		if _, resolved := resolutions[placeholder.Key]; resolved {
			continue
		}
		candidateKeys = append(candidateKeys, placeholder.Key)
	}
	if len(candidateKeys) == 0 {
		return nil
	}

	present, err := archive.ClassifyPresentKeys(entries, candidateKeys)
	if err != nil {
		return fmt.Errorf("classify declared placeholders for %s: %w", toolName, err)
	}
	var missing []string
	for _, key := range candidateKeys {
		if _, ok := present[key]; ok {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("archive preflight: %w", &MissingResolutionsError{Tool: toolName, Keys: missing})
}

func preflightStagingDirs(present []tool.Target, targetPath string) error {
	var errs []error
	for _, target := range present {
		for _, dir := range target.Workspace.PreflightDirs(targetPath) {
			if _, err := archive.StagingTempPath(dir + "/.cc-port-preflight-check"); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("staging filesystem check: %w", errors.Join(errs...))
	}
	return nil
}

// stageEntries routes every present tool's entries to its Stage method.
func stageEntries(
	ctx context.Context,
	present []tool.Target,
	entriesByTool map[string][]archive.RawEntry,
	targetPath string,
	resolutionsByTool map[string]map[string]string,
	extractPhase progress.PhaseHandle,
) (*archive.StagedSet, error) {
	stagedSet := &archive.StagedSet{}
	aggregate := &archive.AggregateCounter{}
	for _, target := range present {
		name := target.Tool.Name()
		for _, raw := range entriesByTool[name] {
			if err := ctx.Err(); err != nil {
				return nil, cleanupStaged(stagedSet, err)
			}
			staged, err := target.Workspace.Stage(
				ctx, targetPath, raw.Entry.WithAggregateCounter(aggregate), resolutionsByTool[name],
			)
			for _, stagedEntry := range staged {
				stagedSet.Add(stagedEntry)
			}
			if err != nil {
				return nil, cleanupStaged(stagedSet, fmt.Errorf("stage %s entry %s: %w", name, raw.Entry.Name, err))
			}
			extractPhase.Advance(1)
		}
	}
	return stagedSet, nil
}

func cleanupStaged(stagedSet *archive.StagedSet, original error) error {
	var cleanupErrors []error
	for _, staged := range stagedSet.All() {
		if err := os.Remove(staged.Temp); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove staging temp %q: %w", staged.Temp, err))
		}
	}
	if len(cleanupErrors) == 0 {
		return original
	}
	return errors.Join(original, errors.Join(cleanupErrors...))
}
