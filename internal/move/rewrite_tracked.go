package move

import "github.com/it-bens/cc-port/internal/rewrite"

// rewriteTracked performs the save → byte-replace → atomic-write sandwich used
// by every uniform plain-bytes rewrite. The tracker snapshots the original
// bytes and mode so a later failure can restore them; errors propagate
// verbatim so callers can wrap with per-group context.
func rewriteTracked(path, oldPath, newPath string, tracker *globalFileTracker) error {
	original, mode, err := tracker.save(path)
	if err != nil {
		return err
	}
	rewritten, _ := rewrite.ReplacePathInBytes(original, oldPath, newPath)
	return rewrite.SafeWriteFile(path, rewritten, mode)
}
