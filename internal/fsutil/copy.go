// Package fsutil provides filesystem utility functions.
package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
)

// CopyDir recursively copies source to destination, preserving file permissions.
func CopyDir(source, destination string) error {
	return filepath.WalkDir(source, func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relativePath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destinationPath := filepath.Join(destination, relativePath)

		if dirEntry.IsDir() {
			return os.MkdirAll(destinationPath, 0750)
		}

		fileData, err := os.ReadFile(path) //nolint:gosec // G304: walked entry under caller-supplied source
		if err != nil {
			return err
		}

		fileInfo, err := dirEntry.Info()
		if err != nil {
			return err
		}
		// Preserve the source file's mode bits so executables and read-only files
		// survive the copy intact.
		return os.WriteFile(destinationPath, fileData, fileInfo.Mode()) //nolint:gosec // G703: mode inherited from source
	})
}
