package rewrite

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// StagingSuffix names the sibling staging directory PromoteDir copies into
// before the final rename.
const StagingSuffix = ".cc-port-staging.tmp"

// MarkerSuffix names the sibling marker file PromoteDir writes once a
// promotion completes, recording the exact source path it promoted from.
// This is the structural signal that later distinguishes "destination
// exists because THIS move's own prior attempt already promoted it" from
// a destination that merely happens to pre-exist for an unrelated reason.
// Without it, "destination exists" is indistinguishable from a genuine
// foreign collision, and silently treating it as resumable risks deleting
// a source that was never actually copied anywhere. The marker is a
// sibling of destination, never written inside it, so a physical project
// directory promotion never pollutes the user's own project contents.
const MarkerSuffix = ".cc-port-promoted-from"

// MarkerFreshnessWindow bounds how long a promotion marker is trusted as
// evidence of an in-progress crash recovery. A marker older than this can
// only be a stale leftover from an unrelated, already-fully-completed
// promotion whose cleanup failed (e.g. two unrelated projects, well
// separated in time, that happen to reuse the identical source and
// destination path strings) — the marker's content alone (a deterministic
// path string) cannot distinguish a genuine crash-recovery retry from that
// replay, but the marker's own age can: a real interrupted move is retried
// promptly, well inside this window. An operator returning to a
// long-stalled move after the window sees a clear refusal instead of a
// silent, incorrect resume.
const MarkerFreshnessWindow = 24 * time.Hour

// VerifyPromotedFrom reports whether destination carries a marker recording
// exactly source as the path it was promoted from, written within
// MarkerFreshnessWindow of now.
func VerifyPromotedFrom(source, destination string, now time.Time) (bool, error) {
	markerPath := destination + MarkerSuffix
	info, err := os.Stat(markerPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat promotion marker for %s: %w", destination, err)
	}
	if now.Sub(info.ModTime()) > MarkerFreshnessWindow {
		return false, nil
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
	if err := os.Remove(destination + MarkerSuffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
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
			_ = os.Remove(destination + MarkerSuffix)
			return os.RemoveAll(destination)
		}
		return os.RemoveAll(staging)
	})
	if err := copyDir(ctx, source, staging, nil); err != nil {
		return fmt.Errorf("stage copy to %s: %w", staging, err)
	}
	if err := os.Rename(staging, destination); err != nil {
		return fmt.Errorf("promote %s to %s: %w", staging, destination, err)
	}
	promoted = true
	if err := os.WriteFile(destination+MarkerSuffix, []byte(source), 0o600); err != nil {
		return fmt.Errorf("write promotion marker for %s: %w", destination, err)
	}
	return nil
}
