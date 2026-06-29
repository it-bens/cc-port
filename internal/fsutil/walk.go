package fsutil

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
)

// ListFilesRecursive returns every file path under dir, skipping directories.
// ctx is checked at the top of every WalkDir callback so a canceled context
// aborts a long enumeration within one iteration.
func ListFilesRecursive(ctx context.Context, dir string) ([]string, error) {
	var files []string
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, walkErr)
	}
	return files, nil
}
