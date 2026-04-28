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
	"time"

	"github.com/it-bens/cc-port/internal/claude"
	"github.com/it-bens/cc-port/internal/manifest"
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
// what ExecutePush would do.
//
//nolint:gocritic // hugeParam: by-value PushOptions matches the spec signature; bodies land in Task 6.
func PlanPush(_ context.Context, _ PushOptions) (*PushPlan, error) {
	return nil, errors.New("sync.PlanPush: not implemented")
}

// ExecutePush runs the export-side pipeline and uploads the archive to
// the remote. Caller passes the plan returned by PlanPush.
//
//nolint:gocritic // hugeParam: by-value PushOptions matches the spec signature; bodies land in Task 6.
func ExecutePush(_ context.Context, _ PushOptions, _ *PushPlan) error {
	return errors.New("sync.ExecutePush: not implemented")
}

// PlanPull reads the remote archive's manifest and returns a PullPlan
// describing what ExecutePull would do.
//
//nolint:gocritic // hugeParam: by-value PullOptions matches the spec signature; bodies land in Task 7.
func PlanPull(_ context.Context, _ PullOptions) (*PullPlan, error) {
	return nil, errors.New("sync.PlanPull: not implemented")
}

// ExecutePull runs the import-side pipeline and applies the archive
// locally. Caller passes the plan returned by PlanPull.
//
//nolint:gocritic // hugeParam: by-value PullOptions matches the spec signature; bodies land in Task 7.
func ExecutePull(_ context.Context, _ PullOptions, _ *PullPlan) error {
	return errors.New("sync.ExecutePull: not implemented")
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
