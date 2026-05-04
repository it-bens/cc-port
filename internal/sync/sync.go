// Package sync orchestrates cc-port push and pull commands. Plan reads
// remote state and produces a struct describing what would happen;
// Execute commits the read or write. The cmd layer renders the plan
// and decides whether to call Execute based on --apply and --force.
package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"sort"
	"time"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/importer"
	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/pipeline"
)

// PushOptions carries the inputs cmd/cc-port push hands to PlanPush and
// ExecutePush. Cmd opens the prior reader pipeline (passed as *PriorRead to
// PlanPush) and the writer pipeline (passed as io.Writer to ExecutePush).
type PushOptions struct {
	ClaudeHome        *claude.Home
	ProjectPath       string
	Name              string
	Categories        manifest.CategorySet
	Placeholders      []manifest.Placeholder
	Force             bool
	EncryptionEnabled bool
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
// ExecutePull. Cmd opens the reader pipeline once and passes the
// pipeline.Source to both Plan and Execute.
type PullOptions struct {
	ClaudeHome *claude.Home
	Name       string
	TargetPath string
	// HomePath is the recipient's home directory, supplied via cmd resolveHomeAnchor()
	HomePath          string
	Resolutions       map[string]string
	FromManifest      *manifest.Metadata
	EncryptionEnabled bool
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

// PriorRead bundles the pre-opened prior pipeline plus the encrypted-or-not
// observation cmd reads off the encrypt stage. nil signals no prior: either
// the remote object did not exist, or --force suppressed the passphrase
// requirement.
type PriorRead struct {
	Source       pipeline.Source
	WasEncrypted bool
}

// PlanPush reads prior remote state from the pre-opened prior pipeline and
// returns a PushPlan describing what ExecutePush would do. Caller (cmd) opens
// the prior reader, dispatches remote.ErrNotFound and encrypt.ErrPassphraseRequired
// before calling, and passes nil prior when no prior is readable (object
// missing, or --force suppressed the passphrase requirement). The
// context.Context parameter is unused today but kept on the public API to
// mirror ExecutePush and reserve a cancellation seam for future readers.
//
//nolint:gocritic // hugeParam: by-value PushOptions matches the public Plan/Execute contract.
func PlanPush(_ context.Context, opts PushOptions, prior *PriorRead) (*PushPlan, error) {
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

// ExecutePush runs the export-side pipeline. Caller (cmd) opens the writer
// pipeline (encrypt.WriterStage + remote.Sink) and passes the outermost
// writer here. The deferred Close on that writer is owned by cmd and is
// load-bearing: remote.Sink commits the upload on Close.
//
//nolint:gocritic // hugeParam: by-value PushOptions matches the public Plan/Execute contract.
func ExecutePush(ctx context.Context, opts PushOptions, plan *PushPlan, output io.Writer) error {
	if plan == nil {
		return errors.New("sync.ExecutePush: plan is nil")
	}
	if output == nil {
		return errors.New("sync.ExecutePush: output is nil")
	}

	exportOptions := export.Options{
		ProjectPath:  opts.ProjectPath,
		Output:       output,
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

// PlanPull reads the remote archive's manifest from the pre-opened source
// and returns a PullPlan describing what ExecutePull would do. Caller (cmd)
// opens the source, dispatches remote.ErrNotFound and encrypt.ErrPassphraseRequired
// before calling, and owns the defer Close. The context.Context parameter is
// unused today but kept on the public API to mirror ExecutePull and reserve
// a cancellation seam for future readers.
//
//nolint:gocritic // hugeParam: by-value PullOptions matches the public Plan/Execute contract.
func PlanPull(_ context.Context, opts PullOptions, source pipeline.Source) (*PullPlan, error) {
	if opts.Name == "" {
		return nil, errors.New("sync.PlanPull: Name is empty")
	}

	metadata, err := manifest.ReadManifestFromZip(source.ReaderAt, source.Size)
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPull: read manifest: %w", err)
	}

	categories, err := manifest.ApplyCategoryEntries(metadata.Export.Categories)
	if err != nil {
		return nil, fmt.Errorf("sync.PlanPull: parse categories: %w", err)
	}

	plan := &PullPlan{
		Name:                 opts.Name,
		RemotePushedBy:       metadata.SyncPushedBy,
		RemoteEncrypted:      opts.EncryptionEnabled,
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
// Resolve values inside the archive's manifest. Implicit keys (see
// importer.IsImplicitKey) are always treated as resolved because
// importer.Run supplies them. Returns the list of declared keys that have
// no resolution, in alphabetical order.
//
// Honoring the sender's Resolve mirrors cc-port import's non-interactive
// behavior: cmd/cc-port hands the same placeholder.Resolve into the
// importer.ResolvePlaceholders prompter closure when no flag overrides it.
// An operator can still override per key via --resolution.
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
		if importer.IsImplicitKey(placeholder.Key) {
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

// ExecutePull runs importer.Run against the pre-opened source. Caller (cmd)
// opens the source and owns the defer Close. The same source instance is
// passed to PlanPull and ExecutePull; pipeline.Source.ReaderAt supports
// repeated reads, so manifest reads in Plan and body reads in importer.Run
// share one materialized tempfile. The returned *importer.Result carries
// the post-import rules-scan; cmd renders it through renderRulesReport.
//
//nolint:gocritic // hugeParam: by-value PullOptions matches the public Plan/Execute contract.
func ExecutePull(ctx context.Context, opts PullOptions, plan *PullPlan, source pipeline.Source) (*importer.Result, error) {
	if plan == nil {
		return nil, errors.New("sync.ExecutePull: plan is nil")
	}

	// Pure merge of manifest-derived defaults and the caller-supplied
	// opts.Resolutions; flag/prompted values overlay manifest values per key.
	// importer.ResolvePlaceholders is the wrong tool here: it does not know
	// about opts.Resolutions and would error on keys covered only by --resolution
	// (no --from-manifest) when no prompter is supplied. computeUnresolved
	// already enforced that every declared key is covered for --apply.
	merged := make(map[string]string)
	if opts.FromManifest != nil {
		for _, placeholder := range opts.FromManifest.Placeholders {
			if importer.IsImplicitKey(placeholder.Key) {
				continue
			}
			if placeholder.Resolve == "" {
				continue
			}
			merged[placeholder.Key] = placeholder.Resolve
		}
	}
	for key, value := range opts.Resolutions {
		if value == "" {
			continue
		}
		merged[key] = value
	}

	result, err := importer.Run(ctx, opts.ClaudeHome, importer.Options{
		Source:      source.ReaderAt,
		Size:        source.Size,
		TargetPath:  opts.TargetPath,
		HomePath:    opts.HomePath,
		Resolutions: merged,
	})
	if err != nil {
		return nil, fmt.Errorf("sync.ExecutePull: import: %w", err)
	}
	return result, nil
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
