package rewrite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// StagingSuffix names the sibling staging directory PromoteDir copies into
// before the final rename.
const StagingSuffix = ".cc-port-staging.tmp"

// RollbackSuffix names cc-port's own rollback-artifact suffix: the sibling
// backup file tool.Restorer writes next to a large tracked file before
// overwriting it in place, and the sibling backup directory Codex's move
// surface writes before touching the memories worktree's git baseline.
const RollbackSuffix = ".cc-port-rollback.tmp"

// ImportStagingSuffix names the sibling temp path archive staging writes to
// before atomically promoting an imported entry onto its final destination.
const ImportStagingSuffix = ".cc-port-import.tmp"

// artifactSuffixes are the whole-base-name suffixes IsArtifactPath matches.
// SafeWriteTempPrefix is matched separately, as a prefix rather than a suffix.
var artifactSuffixes = []string{StagingSuffix, RollbackSuffix, ImportStagingSuffix}

// IsArtifactPath reports whether base — a single path component, not a full
// path — names one of cc-port's own transient files or directories.
// Discovery walks must exclude these; they are never tool data.
func IsArtifactPath(base string) bool {
	if strings.HasPrefix(base, SafeWriteTempPrefix) {
		return true
	}
	for _, suffix := range artifactSuffixes {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// MarkerFilename names the promotion marker file written inside a staging
// directory before its rename publishes the destination. Its content records
// the exact source path, distinguishing a completed promotion from a foreign
// destination that merely happens to exist.
const MarkerFilename = ".cc-port-promoted-from"

// VerifyPromotedFrom reports whether destination carries a marker recording
// exactly source as the path it was promoted from.
func VerifyPromotedFrom(source, destination string) (bool, error) {
	markerPath := filepath.Join(destination, MarkerFilename)
	if _, err := os.Stat(markerPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat promotion marker for %s: %w", destination, err)
	}
	data, err := os.ReadFile(markerPath) //nolint:gosec // G304: caller-supplied, already-validated internal paths
	if err != nil {
		return false, fmt.Errorf("read promotion marker for %s: %w", destination, err)
	}
	return string(data) == source, nil
}

// RemoveMarker removes destination's promotion marker once its move fully
// completes (source successfully removed) and the resume signal is no
// longer needed. A missing marker is not an error.
func RemoveMarker(destination string) error {
	if err := os.Remove(filepath.Join(destination, MarkerFilename)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove promotion marker for %s: %w", destination, err)
	}
	return nil
}

type undoRegistrar interface {
	RegisterUndo(func() error)
}

// PromoteDir copies source into a sibling staging directory beside destination,
// then renames the staging directory into destination. The caller supplies
// copyDir so tests can exercise rollback behavior without a filesystem failure.
func PromoteDir(
	ctx context.Context,
	source, destination string,
	undo undoRegistrar,
	copyDir func(context.Context, string, string, func()) error,
) error {
	staging := destination + StagingSuffix
	promoted := false
	undo.RegisterUndo(func() error {
		if promoted {
			return os.RemoveAll(destination)
		}
		return os.RemoveAll(staging)
	})
	if err := copyDir(ctx, source, staging, nil); err != nil {
		return fmt.Errorf("stage copy to %s: %w", staging, err)
	}
	if err := os.WriteFile(filepath.Join(staging, MarkerFilename), []byte(source), 0o600); err != nil {
		return fmt.Errorf("write promotion marker for %s: %w", destination, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("promote %s to %s: %w", staging, destination, err)
	}
	if err := os.Rename(staging, destination); err != nil {
		return fmt.Errorf("promote %s to %s: %w", staging, destination, err)
	}
	promoted = true
	return nil
}
