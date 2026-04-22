package importer

import (
	"context"
	"testing"

	"github.com/it-bens/cc-port/internal/claude"
)

// RunWithRenameHook is a test-only wrapper around Run that installs
// renameHook on Options before invoking Run. Kept in an _test.go file so
// no production caller can depend on it.
func RunWithRenameHook(
	ctx context.Context,
	claudeHome *claude.Home,
	importOptions Options,
	renameHook func(oldpath, newpath string) error,
) error {
	importOptions.renameHook = renameHook
	return Run(ctx, claudeHome, importOptions)
}

// MaxArchiveBytes returns the live aggregate decompressed-size cap.
// Accessor (not a value re-export) so callers see an override set by
// SetMaxArchiveBytes earlier in the same test.
func MaxArchiveBytes() int64 { return maxArchiveUncompressedBytes }

// SetMaxEntryBytes lowers the per-entry decompressed-size cap for the life
// of t. Used by CI-fast sibling tests that exercise the cap-rejection
// guards without materializing the production 512 MiB per-entry threshold.
// t.Cleanup restores the default so later tests see the production value.
func SetMaxEntryBytes(t *testing.T, limit int64) {
	t.Helper()
	original := maxZipEntryBytes
	maxZipEntryBytes = limit
	t.Cleanup(func() { maxZipEntryBytes = original })
}

// SetMaxArchiveBytes is the aggregate-cap counterpart to SetMaxEntryBytes.
func SetMaxArchiveBytes(t *testing.T, limit int64) {
	t.Helper()
	original := maxArchiveUncompressedBytes
	maxArchiveUncompressedBytes = limit
	t.Cleanup(func() { maxArchiveUncompressedBytes = original })
}
