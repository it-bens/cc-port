package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/lock"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/tool"
)

// fileHistoryTool is the wire name of the one tool whose export ever writes
// an entry under fileHistoryEntryPrefix (internal/tool/claude's private
// toolName constant, duplicated here). This package is generic
// orchestration and must never import a tool adapter (see this package's
// AGENTS.md), so the name can't be looked up from the tool contract, and
// tool.Category (Name, Description, DefaultSelected) has no field a tool
// could use to declare a category's bodies opaque — adding one would be a
// new abstraction built for this single case. Scoping the prefix check to
// this literal tool name keeps the adapter knowledge this package is
// forced to embed as narrow as possible: a same-shaped path under any
// other tool (e.g. a hypothetical "codex/file-history/...") is not
// excluded, because no other tool owns this category.
const fileHistoryTool = "claude"

// fileHistoryEntryPrefix is the tool-relative archive path prefix Claude's
// Stage routes to a sibling temp with resolutions == nil (see
// internal/tool/claude/import.go's "file-history/" case): file-history
// snapshot bodies are never placeholder-substituted on import.
const fileHistoryEntryPrefix = "file-history/"

// Options configures an import operation. Source/Size let the importer
// accept any random-access bytes (file, decrypted tempfile, in-memory)
// without owning archive lifecycle: callers open the source, hand it to
// Run, and close it after Run returns.
type Options struct {
	Source     io.ReaderAt
	Size       int64
	TargetPath string
	Caps       archive.Caps

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
	if err := VerifyManifestTools(allTools, metadata); err != nil {
		return nil, err
	}
	blocksByTool := make(map[string]manifest.Tool, len(metadata.Tools))
	for _, block := range metadata.Tools {
		blocksByTool[block.Name] = block
	}

	reader, err := archive.OpenReader(options.Source, options.Size, options.Caps)
	if err != nil {
		return nil, err
	}
	entries, err := reader.RawEntries()
	if err != nil {
		return nil, err
	}
	if err := VerifyEntryTools(allTools, entries); err != nil {
		return nil, err
	}
	entriesByTool := groupEntriesByTool(entries)

	result, present, resolutionsByTool, err := preflightTargets(ctx, targets, options, blocksByTool, entriesByTool)
	if err != nil {
		return nil, err
	}

	if err := preflightStagingDirs(present, options.TargetPath); err != nil {
		return nil, err
	}
	preflightPhase.End("")

	extractPhase := options.Reporter.Phase("extract", int64(len(entries)), progress.UnitEntries)
	stagedSet, err := stageEntries(ctx, present, entriesByTool, options.TargetPath, resolutionsByTool, extractPhase, options.Caps)
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
	ctx context.Context,
	targets []tool.Target,
	options *Options,
	blocksByTool map[string]manifest.Tool,
	entriesByTool map[string][]archive.RawEntry,
) (Result, []tool.Target, map[string]map[string]string, error) {
	result := Result{Warnings: make(map[string][]string)}
	present := make([]tool.Target, 0, len(targets))
	resolutionsByTool := make(map[string]map[string]string, len(targets))

	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return Result{}, nil, nil, err
		}
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
		resolutions, err := MergeResolutions(block, options.FromManifest, anchors)
		if err != nil {
			return Result{}, nil, nil, err
		}
		if err := checkMissingResolutions(ctx, name, block, anchors, resolutions, entriesByTool[name], options.Caps.MaxAggregateBytes); err != nil {
			return Result{}, nil, nil, err
		}
		resolutionsByTool[name] = resolutions
		present = append(present, target)
	}

	// An empty targets list, or a run where every target's tool is absent
	// from the manifest, never calls the classifier at all, so this
	// pre-return check is what catches a canceled context on that path.
	if err := ctx.Err(); err != nil {
		return Result{}, nil, nil, err
	}
	return result, present, resolutionsByTool, nil
}

// VerifyManifestTools hard-fails when the manifest declares a <tool> block
// this binary does not register at all.
func VerifyManifestTools(allTools *tool.Set, metadata *manifest.Metadata) error {
	for _, block := range metadata.Tools {
		if _, ok := allTools.ByName(block.Name); !ok {
			return fmt.Errorf("archive manifest: %w", &manifest.UnregisteredToolError{Tool: block.Name})
		}
	}
	return nil
}

// VerifyEntryTools hard-fails when any archive entry's leading path segment
// names a tool this binary does not register at all.
func VerifyEntryTools(allTools *tool.Set, entries []archive.RawEntry) error {
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

// MergeResolutions merges, for one tool, the sender's own pre-filled
// Resolve values (weakest), any --from-manifest override for that tool
// (stronger), and the target's implicit anchors (strongest — cc-port
// computes these itself and a stale or malicious sender value must never
// override them). Import preflight (preflightTargets) and pull planning
// (sync.PlanPull) use it.
func MergeResolutions(
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
			var implicitKeys, unknown []string
			for _, placeholder := range overrideBlock.Placeholders {
				if placeholder.Resolve == "" {
					continue
				}
				if _, implicit := anchors[placeholder.Key]; implicit {
					implicitKeys = append(implicitKeys, placeholder.Key)
					continue
				}
				if _, ok := declared[placeholder.Key]; !ok {
					unknown = append(unknown, placeholder.Key)
					continue
				}
				resolutions[placeholder.Key] = placeholder.Resolve
			}
			if len(implicitKeys) > 0 {
				sort.Strings(implicitKeys)
				return nil, &ImplicitKeyOverrideError{Tool: block.Name, Keys: implicitKeys, Surface: "--from-manifest"}
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
	ctx context.Context,
	toolName string, block manifest.Tool, anchors, resolutions map[string]string, entries []archive.RawEntry, maxAggregateBytes int64,
) error {
	missing, err := UnresolvedReferencedKeys(ctx, block, anchors, resolutions, entries, maxAggregateBytes)
	if err != nil {
		return fmt.Errorf("%s: %w", toolName, err)
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("archive preflight: %w", &MissingResolutionsError{Tool: toolName, Keys: missing})
}

// UnresolvedReferencedKeys returns the alphabetized declared keys in block
// that lack a resolution AND appear as a bounded reference in at least one
// of entries' bodies. A declared key the archive never embeds does not need
// a resolution, so it is never flagged.
//
// File-history snapshot bodies are excluded from the reference scan before
// classification: cc-port never inspects or rewrites snapshot contents
// (docs/architecture.md §File-history policy), and Stage never substitutes
// placeholders into them either, so a token that appears only inside an
// opaque snapshot will never be rewritten on import regardless of whether
// it is resolved. Counting it as "referenced" would demand a resolution
// for a key the write path will never touch — excluding it keeps the
// closed-placeholder contract honest rather than weakening it.
//
// Import preflight and pull planning share VerifyManifestTools,
// VerifyEntryTools, MergeResolutions, and this classifier. An archive one
// path accepts therefore cannot fail these gates on the other path: anchors
// mark keys the recipient resolves implicitly, while resolutions mark keys
// covered by sender-provided or --from-manifest resolve values.
func UnresolvedReferencedKeys(
	ctx context.Context,
	block manifest.Tool, anchors, resolutions map[string]string, entries []archive.RawEntry, maxAggregateBytes int64,
) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
		return nil, nil
	}

	present, err := archive.ClassifyPresentKeys(ctx, referencableEntries(entries), candidateKeys, maxAggregateBytes)
	if err != nil {
		return nil, fmt.Errorf("classify declared placeholders: %w", err)
	}
	var missing []string
	for _, key := range candidateKeys {
		if _, ok := present[key]; ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

// referencableEntries drops every Claude file-history snapshot entry from
// entries. See UnresolvedReferencedKeys for why opaque snapshot bodies
// never count as a placeholder reference.
func referencableEntries(entries []archive.RawEntry) []archive.RawEntry {
	filtered := make([]archive.RawEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.ToolName == fileHistoryTool && strings.HasPrefix(entry.Entry.Name, fileHistoryEntryPrefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
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
	caps archive.Caps,
) (*archive.StagedSet, error) {
	stagedSet := &archive.StagedSet{}
	aggregate := archive.NewAggregateCounter(caps.MaxAggregateBytes)
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
