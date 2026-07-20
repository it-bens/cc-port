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

// PromotedFrom reports destination's promotion marker, distinguishing an
// absent marker from one that names a different source — a distinction
// VerifyPromotedFrom's bool collapses away and that a caller needing to
// treat "no marker" and "marker names someone else" differently (a
// converged re-run's non-fatal absence versus a foreign-collision refusal)
// cannot otherwise recover. The marker path is Lstat'd, not Stat'd, so a
// symlink there is refused rather than followed: following it would let
// a dangling symlink collapse to the same ENOENT as a genuinely absent marker
// (silently degrading a malformed destination to "absent" instead of
// failing hard), and would let a symlink to attacker-controlled content
// pass as a genuine promotion proof. Anything at the marker path that
// Lstat resolves but that is not a regular file — symlink, directory,
// device, socket — is positive evidence of a malformed or planted
// destination and hard-fails naming the path and its mode; only a
// genuinely missing path is absence. The Lstat and the ReadFile below are
// not atomic, so a regular file at Lstat time could in principle be
// swapped for a symlink before the read. Left open: planting a marker at
// all already requires write access to the destination's encoded
// directory inside the user's own Claude home, and the check is content
// equality against a derivable value (the expected encoded-directory
// path), so the race grants an attacker with that access no capability
// they lack without it. Closing it needs an open-then-fstat with
// O_NOFOLLOW, a non-portable syscall constant this tool's threat model
// does not warrant.
func PromotedFrom(destination string) (source string, present bool, err error) {
	markerPath := filepath.Join(destination, MarkerFilename)
	info, statErr := os.Lstat(markerPath)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("lstat promotion marker for %s: %w", destination, statErr)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("promotion marker %s is not a regular file (mode %s)", markerPath, info.Mode())
	}
	data, err := os.ReadFile(markerPath) //nolint:gosec // G304: Lstat above confirmed a regular file, not a symlink
	if err != nil {
		return "", false, fmt.Errorf("read promotion marker for %s: %w", destination, err)
	}
	return string(data), true, nil
}

// VerifyPromotedFrom reports whether destination carries a marker recording
// exactly source as the path it was promoted from.
func VerifyPromotedFrom(source, destination string) (bool, error) {
	recorded, present, err := PromotedFrom(destination)
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	return recorded == source, nil
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
	// A source that itself carries a top-level entry named MarkerFilename can
	// never be promoted safely: fsutil.CopyDir would silently O_TRUNC it if
	// it is a regular file, error on it if it is a symlink, and collide with
	// MkdirAll if it is a directory. Checking source directly, before any
	// staging state exists, refuses every case uniformly rather than
	// inferring a collision from whatever happens to survive the copy.
	if err := refuseSourceMarkerCollision(source); err != nil {
		return fmt.Errorf("promote %s to %s: %w", source, destination, err)
	}

	staging := destination + StagingSuffix
	promoted := false
	undo.RegisterUndo(func() error {
		if promoted {
			return os.RemoveAll(destination)
		}
		return os.RemoveAll(staging)
	})
	// The marker is written before the copy, not after: a crash mid-copy must
	// still strand a staging directory that carries it, so a later ownership
	// check (marker content == source) can tell a genuine cc-port stranded
	// copy — safe to delete and retry — from a foreign directory that merely
	// collides with the staging suffix.
	if err := os.MkdirAll(staging, 0o755); err != nil { //nolint:gosec // G301: matches fsutil.CopyDir's own destination-root permission
		return fmt.Errorf("create staging directory %s: %w", staging, err)
	}
	markerInfo, err := writeMarkerContained(staging, source)
	if err != nil {
		return fmt.Errorf("write promotion marker for %s: %w", destination, err)
	}
	if err := copyDir(ctx, source, staging, nil); err != nil {
		return fmt.Errorf("stage copy to %s: %w", staging, err)
	}
	// Re-verify through a contained root before trusting the marker.
	// Byte content alone is not proof the copy left it alone — a
	// replacement file can carry identical bytes — so this also confirms
	// the entry at the marker path is still the exact file
	// writeMarkerContained created, via os.SameFile.
	if err := verifyMarkerContained(staging, source, markerInfo); err != nil {
		return fmt.Errorf("promotion marker for %s: %w", destination, err)
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

// refuseSourceMarkerCollision reports an error if source's top level
// contains an entry named MarkerFilename, of any type. os.Lstat, not
// os.Stat, so a symlink is detected by its own name rather than resolved:
// a symlink collision must refuse exactly like a regular-file or directory
// one, not silently disappear into whatever it points at.
func refuseSourceMarkerCollision(source string) error {
	markerPath := filepath.Join(source, MarkerFilename)
	if _, err := os.Lstat(markerPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", markerPath, err)
	}
	return fmt.Errorf(
		"source directory contains a top-level entry named %s, colliding with cc-port's promotion marker", MarkerFilename,
	)
}

// writeMarkerContained creates MarkerFilename inside staging through an
// os.Root opened on staging, mirroring fsutil.CopyDir's own
// destination-root pattern: the write can never traverse a symlink out of
// the staging tree. O_EXCL fails hard on any pre-existing entry of that
// name. staging is always either absent or freshly created at this point:
// the one production caller (internal/tool/claude/move.go's
// applyProjectDirectoryMove) always reconciles a stranded staging
// directory via removeStagingDir immediately before calling in, so an
// EEXIST here is never PromoteDir's own prior write — it is foreign, and
// this makes no attempt to distinguish the two.
func writeMarkerContained(staging, source string) (info os.FileInfo, err error) {
	root, err := os.OpenRoot(staging)
	if err != nil {
		return nil, fmt.Errorf("open staging root %s: %w", staging, err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close staging root %s: %w", staging, closeErr))
		}
	}()

	file, openErr := root.OpenFile(MarkerFilename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if openErr != nil {
		return nil, fmt.Errorf("create marker: %w", openErr)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close marker: %w", closeErr))
		}
	}()
	if _, err := file.WriteString(source); err != nil {
		return nil, fmt.Errorf("write marker: %w", err)
	}
	info, statErr := file.Stat()
	if statErr != nil {
		return nil, fmt.Errorf("stat marker: %w", statErr)
	}
	return info, nil
}

// verifyMarkerContained re-reads MarkerFilename through an os.Root opened on
// staging and fails hard unless both hold: os.SameFile(created, current)
// confirms the marker path still names the exact file writeMarkerContained
// created, and the marker's content still records exactly source. Content
// alone is the weaker check: a replacement file can carry byte-identical
// content, so identity is checked in addition to, not instead of, content.
func verifyMarkerContained(staging, source string, created os.FileInfo) (err error) {
	root, err := os.OpenRoot(staging)
	if err != nil {
		return fmt.Errorf("open staging root %s: %w", staging, err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close staging root %s: %w", staging, closeErr))
		}
	}()

	current, statErr := root.Stat(MarkerFilename)
	if statErr != nil {
		return fmt.Errorf("stat marker: %w", statErr)
	}
	if !os.SameFile(created, current) {
		return fmt.Errorf("marker %s is no longer the file cc-port created; the copy replaced it", MarkerFilename)
	}
	data, err := root.ReadFile(MarkerFilename)
	if err != nil {
		return fmt.Errorf("re-read marker: %w", err)
	}
	if string(data) != source {
		return fmt.Errorf(
			"source directory contains a top-level entry named %s that clobbered the promotion marker", MarkerFilename,
		)
	}
	return nil
}
