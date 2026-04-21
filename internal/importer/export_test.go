package importer

import (
	"context"

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

// MaxArchiveUncompressedBytes re-exports maxArchiveUncompressedBytes so
// tests in the importer_test package can assert against it without hard-
// coding the 4 GiB threshold.
const MaxArchiveUncompressedBytes = maxArchiveUncompressedBytes
