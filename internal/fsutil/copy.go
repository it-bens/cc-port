// Package fsutil provides filesystem utility functions.
package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
)

// CopyDir recursively copies source to destination, preserving file and directory permissions.
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

		entryInfo, err := dirEntry.Info()
		if err != nil {
			return err
		}

		if dirEntry.IsDir() {
			// MkdirAll only applies the mode to directories it creates; WalkDir
			// visits parents before children, so each level lands with its own
			// source mode rather than inheriting from an already-created parent.
			return os.MkdirAll(destinationPath, entryInfo.Mode().Perm())
		}

		fileData, err := os.ReadFile(path) //nolint:gosec // G304: walked entry under caller-supplied source
		if err != nil {
			return err
		}

		// Preserve the source file's mode bits so executables and read-only files
		// survive the copy intact.
		return os.WriteFile(destinationPath, fileData, entryInfo.Mode()) //nolint:gosec // G703: mode inherited from source
	})
}
