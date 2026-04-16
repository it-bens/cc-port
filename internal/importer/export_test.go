package importer

import "github.com/it-bens/cc-port/internal/claude"

// RunWithRenameHook is a test-only wrapper around Run that installs
// renameHook on Options before invoking Run. Kept in an _test.go file so
// no production caller can depend on it.
func RunWithRenameHook(
	claudeHome *claude.Home,
	importOptions Options,
	renameHook func(oldpath, newpath string) error,
) error {
	importOptions.renameHook = renameHook
	return Run(claudeHome, importOptions)
}
