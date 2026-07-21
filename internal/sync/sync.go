// Package sync orchestrates cc-port push and pull commands across every
// selected tool. Plan reads remote state and produces a struct describing
// what would happen; Execute commits the read or write. The cmd layer
// renders the plan and decides whether to call Execute based on --apply
// and --force.
package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/user"
	"time"

	"github.com/it-bens/cc-port/internal/archive"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/progress"
	"github.com/it-bens/cc-port/internal/tool"
)

// now is a seam reassigned under t.Cleanup so tests can pin timestamps.
var now = time.Now

// PushOptions carries the inputs cmd/cc-port push hands to PlanPush and
// ExecutePush. Selected and Placeholders are keyed by tool name.
type PushOptions struct {
	Targets      []tool.Target
	ProjectPath  string
	Name         string
	Selected     map[string]map[string]bool
	Placeholders map[string][]manifest.Placeholder
	// Hostname, Getenv, and CurrentUser are the identity-lookup seams selfPusher uses to derive the
	// cross-machine push identity. Real callers wire os.Hostname, os.Getenv, and user.Current;
	// tests supply fakes.
	Hostname          func() (string, error)
	Getenv            func(string) string
	CurrentUser       func() (*user.User, error)
	Force             bool
	EncryptionEnabled bool

	// Reporter receives the push progress event stream. PlanPush ignores it
	// (it is a pure pre-flight read); ExecutePush defaults a nil Reporter to
	// progress.Noop().
	Reporter progress.Reporter
}

// PushPlan is the read-only result of PlanPush. Render writes it for
// dry-run output; ExecutePush consumes it to perform the upload.
type PushPlan struct {
	Name              string
	SelfPusher        string
	Selected          map[string]map[string]bool
	PriorPushedBy     string
	PriorPushedAt     time.Time
	PriorEncrypted    bool
	PriorSize         int64
	EncryptionEnabled bool
	CrossMachine      bool
}

// PullOptions carries the inputs cmd/cc-port pull hands to PlanPull and
// ExecutePull.
type PullOptions struct {
	AllTools          *tool.Set
	Targets           []tool.Target
	Name              string
	TargetPath        string
	FromManifest      *manifest.Metadata
	EncryptionEnabled bool

	// Reporter receives the pull progress event stream. PlanPull ignores it
	// (it is a pure pre-flight read); ExecutePull defaults a nil Reporter to
	// progress.Noop().
	Reporter progress.Reporter
}

// PullPlan is the read-only result of PlanPull. Render writes it for
// dry-run output; ExecutePull consumes it to perform the import.
type PullPlan struct {
	Name                   string
	RemotePushedBy         string
	RemotePushedAt         time.Time
	RemoteEncrypted        bool
	RemoteSize             int64
	Tools                  []string
	DeclaredPlaceholders   map[string][]manifest.Placeholder
	UnresolvedPlaceholders map[string][]string
}

// PriorRead bundles the pre-opened prior pipeline plus the encrypted-or-not
// observation cmd reads off the encrypt stage. nil signals no prior: either
// the remote object did not exist, or --force suppressed the passphrase
// requirement.
type PriorRead struct {
	Source       pipeline.Source
	WasEncrypted bool
}

// PlanPush reads prior remote state from the pre-opened prior pipeline and
// returns a PushPlan describing what ExecutePush would do.
//
//nolint:gocritic // hugeParam: by-value PushOptions matches the public Plan/Execute contract.
func PlanPush(_ context.Context, opts PushOptions, prior *PriorRead) (*PushPlan, error) {
	if opts.Name == "" {
		return nil, errors.New("sync.PlanPush: Name is empty")
	}
	if opts.Hostname == nil || opts.Getenv == nil || opts.CurrentUser == nil {
		return nil, errors.New("sync.PlanPush: Hostname, Getenv, and CurrentUser identity seams are required")
	}

	pusher, err := selfPusher(opts.Hostname, opts.Getenv, opts.CurrentUser)
	// The injected identity seams must produce a pusher even with --force. An
	// empty SelfPusher would write an empty SyncPushedBy, which another machine
	// reads as no prior pusher and so skips its cross-machine conflict check.
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPush: derive self identity: %w", err)
	}
	plan := &PushPlan{
		Name:              opts.Name,
		SelfPusher:        pusher,
		Selected:          opts.Selected,
		EncryptionEnabled: opts.EncryptionEnabled,
	}

	if prior == nil {
		return plan, nil
	}

	plan.PriorSize = prior.Source.Size
	plan.PriorEncrypted = prior.WasEncrypted

	priorMetadata, err := manifest.ReadManifestFromZip(prior.Source.ReaderAt, prior.Source.Size)
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPush: read prior manifest: %w", err)
	}
	plan.PriorPushedBy = priorMetadata.SyncPushedBy
	if priorMetadata.SyncPushedAt != "" {
		parsed, err := time.Parse(time.RFC3339, priorMetadata.SyncPushedAt)
		if err != nil {
			return nil, fmt.Errorf("sync.PlanPush: parse prior SyncPushedAt %q: %w", priorMetadata.SyncPushedAt, err)
		}
		plan.PriorPushedAt = parsed
	}
	plan.CrossMachine = plan.PriorPushedBy != "" && plan.PriorPushedBy != plan.SelfPusher

	return plan, nil
}

// ExecutePush runs the export-side pipeline.
//
//nolint:gocritic // hugeParam: by-value PushOptions matches the public Plan/Execute contract.
func ExecutePush(ctx context.Context, opts PushOptions, plan *PushPlan, output io.Writer) (*export.Result, error) {
	if plan == nil {
		return nil, errors.New("sync.ExecutePush: plan is nil")
	}
	if output == nil {
		return nil, errors.New("sync.ExecutePush: output is nil")
	}
	if opts.Reporter == nil {
		opts.Reporter = progress.Noop()
	}

	exportPhase := opts.Reporter.Phase("export", 0, progress.UnitItems)
	exportOptions := export.Options{
		ProjectPath:  opts.ProjectPath,
		Output:       output,
		Selected:     opts.Selected,
		Placeholders: opts.Placeholders,
		SyncPushedBy: plan.SelfPusher,
		SyncPushedAt: now().UTC(),
		Reporter:     exportPhase,
	}
	result, err := export.Run(ctx, opts.Targets, &exportOptions)
	if err != nil {
		return nil, fmt.Errorf("sync.ExecutePush: export: %w", err)
	}
	exportPhase.End("")
	return &result, nil
}

// PlanPull reads the remote archive's manifest from the pre-opened source
// and returns a PullPlan describing what ExecutePull would do.
//
//nolint:gocritic // hugeParam: by-value PullOptions matches the public Plan/Execute contract.
func PlanPull(ctx context.Context, opts PullOptions, source pipeline.Source) (*PullPlan, error) {
	if opts.Name == "" {
		return nil, errors.New("sync.PlanPull: Name is empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	caps := archive.DefaultCaps()
	metadata, err := manifest.ReadManifestFromZip(source.ReaderAt, source.Size)
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPull: read manifest: %w", err)
	}
	if err := importer.VerifyManifestTools(opts.AllTools, metadata); err != nil {
		return nil, fmt.Errorf("sync.PlanPull: verify manifest tools: %w", err)
	}

	reader, err := archive.OpenReader(source.ReaderAt, source.Size, caps)
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPull: open archive: %w", err)
	}
	entries, err := reader.RawEntries()
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPull: read archive entries: %w", err)
	}
	if err := importer.VerifyEntryTools(opts.AllTools, entries); err != nil {
		return nil, fmt.Errorf("sync.PlanPull: verify entry tools: %w", err)
	}
	entriesByTool := make(map[string][]archive.RawEntry, len(entries))
	for _, entry := range entries {
		entriesByTool[entry.ToolName] = append(entriesByTool[entry.ToolName], entry)
	}

	plan := &PullPlan{
		Name:                   opts.Name,
		RemotePushedBy:         metadata.SyncPushedBy,
		RemoteEncrypted:        opts.EncryptionEnabled,
		RemoteSize:             source.Size,
		DeclaredPlaceholders:   make(map[string][]manifest.Placeholder),
		UnresolvedPlaceholders: make(map[string][]string),
	}
	if metadata.SyncPushedAt != "" {
		parsed, err := time.Parse(time.RFC3339, metadata.SyncPushedAt)
		if err != nil {
			return nil, fmt.Errorf("sync.PlanPull: parse SyncPushedAt %q: %w", metadata.SyncPushedAt, err)
		}
		plan.RemotePushedAt = parsed
	}

	targetsByName := make(map[string]tool.Target, len(opts.Targets))
	for _, target := range opts.Targets {
		targetsByName[target.Tool.Name()] = target
	}

	for _, block := range metadata.Tools {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		plan.Tools = append(plan.Tools, block.Name)
		plan.DeclaredPlaceholders[block.Name] = block.Placeholders

		target, ok := targetsByName[block.Name]
		if !ok {
			continue
		}
		anchors, err := target.Workspace.ImplicitAnchors(opts.TargetPath)
		if err != nil {
			return nil, fmt.Errorf("sync.PlanPull: implicit anchors for %s: %w", block.Name, err)
		}
		resolutions, err := importer.MergeResolutions(block, opts.FromManifest, anchors)
		if err != nil {
			return nil, fmt.Errorf("sync.PlanPull: merge resolutions for %s: %w", block.Name, err)
		}
		unresolved, err := importer.UnresolvedReferencedKeys(
			ctx, block, anchors, resolutions, entriesByTool[block.Name], caps.MaxAggregateBytes,
		)
		if err != nil {
			return nil, fmt.Errorf("sync.PlanPull: unresolved placeholders for %s: %w", block.Name, err)
		}
		plan.UnresolvedPlaceholders[block.Name] = unresolved
	}

	// A manifest with no tool blocks, or where every block's target was
	// skipped above, never calls the classifier at all, so this pre-return
	// check is what catches a canceled context on that path.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return plan, nil
}

// ExecutePull runs importer.Run against the pre-opened source.
//
//nolint:gocritic // hugeParam: by-value PullOptions matches the public Plan/Execute contract.
func ExecutePull(ctx context.Context, opts PullOptions, plan *PullPlan, source pipeline.Source) (*importer.Result, error) {
	if plan == nil {
		return nil, errors.New("sync.ExecutePull: plan is nil")
	}
	if opts.Reporter == nil {
		opts.Reporter = progress.Noop()
	}

	importPhase := opts.Reporter.Phase("import", 0, progress.UnitItems)
	result, err := importer.Run(ctx, opts.AllTools, opts.Targets, &importer.Options{
		Source:       source.ReaderAt,
		Size:         source.Size,
		TargetPath:   opts.TargetPath,
		Caps:         archive.DefaultCaps(),
		FromManifest: opts.FromManifest,
		Reporter:     importPhase,
	})
	if err != nil {
		return nil, fmt.Errorf("sync.ExecutePull: import: %w", err)
	}
	importPhase.End("")
	return result, nil
}

// Sentinel errors surfaced by Plan and Execute. See README §Plan-and-execute split.
var (
	ErrCrossMachineConflict  = errors.New("sync: remote was last pushed by a different machine")
	ErrRemoteNotFound        = errors.New("sync: archive not found on remote")
	ErrPassphraseRequired    = errors.New("sync: archive is encrypted; pass --passphrase-env or --passphrase-file")
	ErrUnresolvedPlaceholder = errors.New("sync: archive declares placeholders not covered by --from-manifest")
)

// selfPusher returns "hostname-username" for the current invocation.
func selfPusher(hostname func() (string, error), getenv func(string) string, currentUser func() (*user.User, error)) (string, error) {
	host, err := hostname()
	if err != nil {
		return "", fmt.Errorf("sync: read hostname for cross-machine identity: %w", err)
	}
	if host == "" {
		return "", errors.New(
			"sync: hostname is empty; cross-machine identity cannot be determined",
		)
	}
	name := getenv("USER")
	if name == "" {
		u, err := currentUser()
		if err != nil {
			return "", fmt.Errorf("sync: resolve current user for cross-machine identity: %w", err)
		}
		name = u.Username
	}
	if name == "" {
		return "", errors.New(
			"sync: current username is empty; cross-machine identity cannot be determined",
		)
	}
	return host + "-" + name, nil
}
