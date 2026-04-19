package fsutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveExistingAncestor walks absDir upward to the longest prefix that
// exists on disk, evaluates symlinks on that prefix, and re-attaches any
// missing trailing components unchanged. Callers like os.MkdirAll can
// then create the missing tail on the resolved filesystem — essential
// when a destination's parent is a symlink crossing a filesystem
// boundary and os.Rename must stay intra-filesystem.
//
// Contract: absDir MUST be an absolute path. Passing a relative path is
// a programmer error at the caller's layer, not an operational error,
// so the helper panics rather than silently Abs-ifying or returning a
// checkable error. A panic surfaces the misuse at the exact call site
// before production runs into a surprising CWD-relative result.
func ResolveExistingAncestor(absDir string) (string, error) {
	if !filepath.IsAbs(absDir) {
		panic(fmt.Sprintf("fsutil.ResolveExistingAncestor: path must be absolute, got %q", absDir))
	}

	existingPrefix := absDir
	var missingSuffix string
	for {
		if _, err := os.Lstat(existingPrefix); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat %q: %w", existingPrefix, err)
		}
		if existingPrefix == "/" {
			break
		}
		parent, child := filepath.Split(existingPrefix)
		existingPrefix = strings.TrimSuffix(parent, "/")
		if existingPrefix == "" {
			existingPrefix = "/"
		}
		if missingSuffix == "" {
			missingSuffix = child
		} else {
			missingSuffix = filepath.Join(child, missingSuffix)
		}
	}

	resolvedPrefix, err := filepath.EvalSymlinks(existingPrefix)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks for %q: %w", existingPrefix, err)
	}

	if missingSuffix == "" {
		return resolvedPrefix, nil
	}
	return filepath.Join(resolvedPrefix, missingSuffix), nil
}
