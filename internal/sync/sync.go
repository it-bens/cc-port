// Package sync orchestrates cc-port push and pull commands. Plan reads
// remote state and produces a struct describing what would happen;
// Execute commits the read or write. The cmd layer renders the plan
// and decides whether to call Execute based on --apply and --force.
package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"sort"
	"time"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/encrypt"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
	"github.com/it-bens/cc-port/internal/remote"
)

// PushOptions carries the inputs cmd/cc-port push hands to PlanPush and
// ExecutePush.
type PushOptions struct {
	ClaudeHome   *claude.Home
	ProjectPath  string
	Remote       *remote.Remote
	Name         string
	Categories   manifest.CategorySet
	Placeholders []manifest.Placeholder
	Passphrase   string
	Force        bool
}

// PushPlan is the read-only result of PlanPush. Render writes it for
// dry-run output; ExecutePush consumes it to perform the upload.
type PushPlan struct {
	Name              string
	SelfPusher        string
	Categories        manifest.CategorySet
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
	ClaudeHome   *claude.Home
	Remote       *remote.Remote
	Name         string
	TargetPath   string
	Resolutions  map[string]string
	FromManifest *manifest.Metadata
	Passphrase   string
}

// PullPlan is the read-only result of PlanPull. Render writes it for
// dry-run output; ExecutePull consumes it to perform the import.
type PullPlan struct {
	Name                   string
	RemotePushedBy         string
	RemotePushedAt         time.Time
	RemoteEncrypted        bool
	RemoteSize             int64
	Categories             manifest.CategorySet
	DeclaredPlaceholders   []manifest.Placeholder
	UnresolvedPlaceholders []string
}

// PlanPush reads prior remote state and returns a PushPlan describing
// what ExecutePush would do. Composes a permissive read pipeline so a
// plaintext prior is silently accepted even when the operator's
// passphrase targets the new archive being written.
//
//nolint:gocritic // hugeParam: by-value PushOptions matches the public Plan/Execute contract.
func PlanPush(ctx context.Context, opts PushOptions) (*PushPlan, error) {
	if opts.Remote == nil {
		return nil, errors.New("sync.PlanPush: Remote is nil")
	}
	if opts.Name == "" {
		return nil, errors.New("sync.PlanPush: Name is empty")
	}

	pusher, err := selfPusher()
	if err != nil && !opts.Force {
		return nil, fmt.Errorf("sync.PlanPush: derive self identity: %w", err)
	}
	// Force suppresses the selfPusher error: the new archive's
	// <sync-pushed-by> is left empty (omitempty drops the element)
	// and the cross-machine check below trivially passes.
	plan := &PushPlan{
		Name:              opts.Name,
		SelfPusher:        pusher,
		Categories:        opts.Categories,
		EncryptionEnabled: opts.Passphrase != "",
	}

	priorReadStage := &encrypt.ReaderStage{Pass: opts.Passphrase, Mode: encrypt.Permissive}
	source, err := pipeline.RunReader(ctx, []pipeline.ReaderStage{
		&remote.Source{Remote: opts.Remote, Key: opts.Name},
		priorReadStage,
	})
	if errors.Is(err, remote.ErrNotFound) {
		return plan, nil
	}
	if errors.Is(err, encrypt.ErrPassphraseRequired) {
		if opts.Force {
			// No prior-read possible without the passphrase. Plan
			// records nothing about the prior; cmd-layer overwrites
			// on apply. Operator accepted the trade-off via --force.
			return plan, nil
		}
		return nil, fmt.Errorf(
			"sync.PlanPush: prior remote is encrypted, passphrase required for conflict detection (or use --force): %w",
			ErrPassphraseRequired,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPush: read prior remote: %w", err)
	}
	defer func() { _ = source.Close() }()

	plan.PriorSize = source.Size
	plan.PriorEncrypted = priorReadStage.WasEncrypted()

	priorMetadata, err := manifest.ReadManifestFromZip(source.ReaderAt, source.Size)
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPush: read prior manifest: %w", err)
	}
	plan.PriorPushedBy = priorMetadata.SyncPushedBy
	if priorMetadata.SyncPushedAt != "" {
		parsed, err := time.Parse(time.RFC3339, priorMetadata.SyncPushedAt)
		if err != nil {
			return nil, fmt.Errorf(
				"sync.PlanPush: parse prior SyncPushedAt %q: %w",
				priorMetadata.SyncPushedAt, err,
			)
		}
		plan.PriorPushedAt = parsed
	}
	plan.CrossMachine = plan.PriorPushedBy != "" && plan.PriorPushedBy != plan.SelfPusher

	return plan, nil
}

// ExecutePush runs the export-side pipeline and uploads the archive to
// the remote. Caller passes the plan returned by PlanPush. The deferred
// out.Close error capture is load-bearing: remote.Sink commits the
// upload inside Close, so a failed commit must surface as a returned
// error.
//
//nolint:gocritic // hugeParam: by-value PushOptions matches the public Plan/Execute contract.
func ExecutePush(ctx context.Context, opts PushOptions, plan *PushPlan) (err error) {
	if opts.Remote == nil {
		return errors.New("sync.ExecutePush: Remote is nil")
	}
	if plan == nil {
		return errors.New("sync.ExecutePush: plan is nil")
	}

	out, err := pipeline.RunWriter(ctx, []pipeline.WriterStage{
		&encrypt.WriterStage{Pass: opts.Passphrase},
		&remote.Sink{Remote: opts.Remote, Key: opts.Name},
	})
	if err != nil {
		return fmt.Errorf("sync.ExecutePush: build output pipeline: %w", err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("sync.ExecutePush: close output pipeline: %w", cerr))
		}
	}()

	exportOptions := export.Options{
		ProjectPath:  opts.ProjectPath,
		Output:       out,
		Categories:   opts.Categories,
		Placeholders: opts.Placeholders,
		SyncPushedBy: plan.SelfPusher,
		SyncPushedAt: time.Now().UTC(),
	}
	if _, err := export.Run(ctx, opts.ClaudeHome, &exportOptions); err != nil {
		return fmt.Errorf("sync.ExecutePush: export: %w", err)
	}
	return nil
}

// PlanPull reads the remote archive's manifest and returns a PullPlan
// describing what ExecutePull would do. Composes the strict read
// pipeline so an encrypted-no-pass or plaintext-with-pass mismatch
// surfaces before the manifest is parsed.
//
//nolint:gocritic // hugeParam: by-value PullOptions matches the public Plan/Execute contract.
func PlanPull(ctx context.Context, opts PullOptions) (plan *PullPlan, err error) {
	if opts.Remote == nil {
		return nil, errors.New("sync.PlanPull: Remote is nil")
	}
	if opts.Name == "" {
		return nil, errors.New("sync.PlanPull: Name is empty")
	}

	source, err := pipeline.RunReader(ctx, []pipeline.ReaderStage{
		&remote.Source{Remote: opts.Remote, Key: opts.Name},
		&encrypt.ReaderStage{Pass: opts.Passphrase, Mode: encrypt.Strict},
	})
	if errors.Is(err, remote.ErrNotFound) {
		return nil, ErrRemoteNotFound
	}
	if errors.Is(err, encrypt.ErrPassphraseRequired) {
		return nil, ErrPassphraseRequired
	}
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPull: open remote source: %w", err)
	}
	defer func() {
		if cerr := source.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("sync.PlanPull: close remote source: %w", cerr))
		}
	}()

	metadata, err := manifest.ReadManifestFromZip(source.ReaderAt, source.Size)
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPull: read manifest: %w", err)
	}

	categories, err := manifest.ApplyCategoryEntries(metadata.Export.Categories)
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPull: parse categories: %w", err)
	}

	plan = &PullPlan{
		Name:                 opts.Name,
		RemotePushedBy:       metadata.SyncPushedBy,
		RemoteEncrypted:      opts.Passphrase != "",
		RemoteSize:           source.Size,
		Categories:           categories,
		DeclaredPlaceholders: metadata.Placeholders,
	}
	if metadata.SyncPushedAt != "" {
		parsed, err := time.Parse(time.RFC3339, metadata.SyncPushedAt)
		if err != nil {
			return nil, fmt.Errorf(
				"sync.PlanPull: parse SyncPushedAt %q: %w",
				metadata.SyncPushedAt, err,
			)
		}
		plan.RemotePushedAt = parsed
	}

	plan.UnresolvedPlaceholders = computeUnresolved(
		metadata.Placeholders, opts.Resolutions, opts.FromManifest, opts.TargetPath,
	)

	return plan, nil
}

// computeUnresolved diffs the archive's declared placeholders against
// every available source of resolution: the caller's --resolution map,
// the optional --from-manifest metadata, and the sender's own pre-filled
// Resolve values inside the archive's manifest. The implicit
// {{PROJECT_PATH}} (importer.ProjectPathKey) is always treated as
// resolved because importer.Run injects it from TargetPath. Returns the
// list of declared keys that have no resolution, in alphabetical order.
//
// Honoring the sender's Resolve mirrors cc-port import's non-interactive
// behavior (cmd/cc-port/importcmd.go:promptImportResolutions uses
// placeholder.Resolve when no flag overrides it). An operator can still
// override per key via --resolution.
func computeUnresolved(
	declared []manifest.Placeholder,
	resolutions map[string]string,
	fromManifest *manifest.Metadata,
	_ string,
) []string {
	covered := make(map[string]bool, len(declared))
	for _, placeholder := range declared {
		if placeholder.Resolve != "" {
			covered[placeholder.Key] = true
		}
	}
	for key, value := range resolutions {
		if value != "" {
			covered[key] = true
		}
	}
	if fromManifest != nil {
		for _, placeholder := range fromManifest.Placeholders {
			if placeholder.Resolve != "" {
				covered[placeholder.Key] = true
			}
		}
	}
	covered[importer.ProjectPathKey] = true

	var missing []string
	for _, placeholder := range declared {
		if placeholder.Resolvable != nil && !*placeholder.Resolvable {
			continue
		}
		if !covered[placeholder.Key] {
			missing = append(missing, placeholder.Key)
		}
	}
	sort.Strings(missing)
	return missing
}

// ExecutePull runs the import-side pipeline and applies the archive
// locally. Caller passes the plan returned by PlanPull. The deferred
// source.Close error capture is load-bearing: the read pipeline owns a
// 0600 tempfile whose removal must surface to the caller.
//
//nolint:gocritic // hugeParam: by-value PullOptions matches the public Plan/Execute contract.
func ExecutePull(ctx context.Context, opts PullOptions, plan *PullPlan) (err error) {
	if opts.Remote == nil {
		return errors.New("sync.ExecutePull: Remote is nil")
	}
	if plan == nil {
		return errors.New("sync.ExecutePull: plan is nil")
	}

	source, err := pipeline.RunReader(ctx, []pipeline.ReaderStage{
		&remote.Source{Remote: opts.Remote, Key: opts.Name},
		&encrypt.ReaderStage{Pass: opts.Passphrase, Mode: encrypt.Strict},
	})
	if errors.Is(err, remote.ErrNotFound) {
		return ErrRemoteNotFound
	}
	if errors.Is(err, encrypt.ErrPassphraseRequired) {
		return ErrPassphraseRequired
	}
	if err != nil {
		return fmt.Errorf("sync.ExecutePull: open remote source: %w", err)
	}
	defer func() {
		if cerr := source.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("sync.ExecutePull: close remote source: %w", cerr))
		}
	}()

	merged := mergeResolutions(opts.FromManifest, opts.Resolutions)

	if err := importer.Run(ctx, opts.ClaudeHome, importer.Options{
		Source:      source.ReaderAt,
		Size:        source.Size,
		TargetPath:  opts.TargetPath,
		Resolutions: merged,
	}); err != nil {
		return fmt.Errorf("sync.ExecutePull: import: %w", err)
	}
	return nil
}

// mergeResolutions builds the resolutions map ExecutePull hands to
// importer.Run. The sender-supplied {{PROJECT_PATH}} is dropped because
// importer.Run injects it from TargetPath; a sender resolve would point
// at the sender's disk and silently misroute every reference in the
// pulled bodies. Same refusal as cmd/cc-port/parseResolutionFlags.
// Empty Resolve values are skipped so importer.ValidateResolutions does
// not see a phantom empty entry for a key the operator never resolved.
// Flag values overlay manifest values per key.
func mergeResolutions(fromManifest *manifest.Metadata, flagResolutions map[string]string) map[string]string {
	merged := make(map[string]string)
	if fromManifest != nil {
		for _, placeholder := range fromManifest.Placeholders {
			if placeholder.Key == importer.ProjectPathKey {
				continue
			}
			if placeholder.Resolve == "" {
				continue
			}
			merged[placeholder.Key] = placeholder.Resolve
		}
	}
	for key, value := range flagResolutions {
		if value != "" {
			merged[key] = value
		}
	}
	return merged
}

// Sentinel errors surfaced by Plan and Execute. See README §Plan-and-execute split.
var (
	ErrCrossMachineConflict  = errors.New("sync: remote was last pushed by a different machine")
	ErrRemoteNotFound        = errors.New("sync: archive not found on remote")
	ErrPassphraseRequired    = errors.New("sync: archive is encrypted; pass --passphrase-env or --passphrase-file")
	ErrUnresolvedPlaceholder = errors.New("sync: archive declares placeholders not covered by --resolution or --from-manifest")
)

// selfPusher returns "hostname-username" for the current invocation.
// Used as the SyncPushedBy field when push commits, and as the
// equality target when reading prior remote metadata for the
// cross-machine check. Refuses empty hostname or empty username:
// silent fallbacks like "unknown-host-unknown-user" would collapse
// every misconfigured machine into the same identity and silently
// false-negate the cross-machine refusal. Operators on machines
// where Hostname or USER cannot be determined override with --force.
func selfPusher() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("sync: read hostname for cross-machine identity: %w", err)
	}
	if host == "" {
		return "", errors.New(
			"sync: hostname is empty; cross-machine identity cannot be determined " +
				"(set $HOSTNAME or use --force to override)",
		)
	}
	name := os.Getenv("USER")
	if name == "" {
		u, err := user.Current()
		if err != nil {
			return "", fmt.Errorf("sync: resolve current user for cross-machine identity: %w", err)
		}
		name = u.Username
	}
	if name == "" {
		return "", errors.New(
			"sync: current username is empty; cross-machine identity cannot be determined " +
				"(set $USER or use --force to override)",
		)
	}
	return host + "-" + name, nil
}
