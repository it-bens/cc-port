// Package fsutil provides filesystem utility functions.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// CopyDir recursively copies source to destination, preserving file and
// directory permissions. Symlinks are replicated as symlinks — their
// target string is written verbatim and never followed for content.
// Regular files are streamed via io.Copy to avoid loading them whole
// into memory. Irregular entries (sockets, FIFOs, devices) cause the
// copy to fail-hard.
//
// Writes go through an os.Root opened on destination, so even a
// malformed relative path cannot land outside destination.
func CopyDir(source, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil { //nolint:gosec // G301: cc-port-wide 0o755 convention
		return fmt.Errorf("create destination %q: %w", destination, err)
	}

	destRoot, err := os.OpenRoot(destination)
	if err != nil {
		return fmt.Errorf("open destination root %q: %w", destination, err)
	}
	defer func() { _ = destRoot.Close() }()

	return filepath.WalkDir(source, func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relativePath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relativePath == "." {
			return nil
		}

		entryMode := dirEntry.Type()

		switch {
		case entryMode&fs.ModeSymlink != 0:
			return copySymlink(path, destRoot, relativePath)

		case dirEntry.IsDir():
			return copyDirectory(dirEntry, destRoot, relativePath)

		case entryMode.IsRegular():
			info, err := dirEntry.Info()
			if err != nil {
				return err
			}
			if parent := filepath.Dir(relativePath); parent != "." {
				if err := destRoot.MkdirAll(parent, 0o755); err != nil {
					return fmt.Errorf("create parent for %q: %w", relativePath, err)
				}
			}
			return streamRegularFile(path, destRoot, relativePath, info.Mode().Perm())

		default:
			return fmt.Errorf("fsutil: cannot copy irregular entry %q (mode %s)", path, entryMode)
		}
	})
}

// copySymlink replicates a source symlink at relativePath under destRoot.
// The target string is read via os.Readlink and written verbatim — the
// symlink is never followed for content.
func copySymlink(sourcePath string, destRoot *os.Root, relativePath string) error {
	target, err := os.Readlink(sourcePath)
	if err != nil {
		return fmt.Errorf("read symlink %q: %w", sourcePath, err)
	}
	if parent := filepath.Dir(relativePath); parent != "." {
		if err := destRoot.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("create parent for symlink %q: %w", relativePath, err)
		}
	}
	if err := destRoot.Symlink(target, relativePath); err != nil {
		return fmt.Errorf("create symlink %q: %w", relativePath, err)
	}
	return nil
}

// copyDirectory creates relativePath under destRoot at the source entry's
// mode. MkdirAll does not re-chmod an existing directory, so if a parent
// of this entry was created earlier in the walk at a coarser mode, tighten
// it now via Chmod.
func copyDirectory(dirEntry fs.DirEntry, destRoot *os.Root, relativePath string) error {
	info, err := dirEntry.Info()
	if err != nil {
		return err
	}
	if err := destRoot.MkdirAll(relativePath, info.Mode().Perm()); err != nil {
		return fmt.Errorf("create directory %q: %w", relativePath, err)
	}
	if err := destRoot.Chmod(relativePath, info.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod directory %q: %w", relativePath, err)
	}
	return nil
}

// openStreamDest is the factory CopyDir uses to create each destination file.
// It is a package-level var so internal tests can substitute a wrapper that
// returns a synthetic error on Close — exercising the deferred close-error
// propagation without requiring a flaky real-world disk-full condition.
var openStreamDest = func(root *os.Root, path string, mode os.FileMode) (io.WriteCloser, error) {
	return root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
}

// streamRegularFile copies src's contents to relativePath under destRoot
// and applies mode afterward. Uses io.Copy so large files do not land
// whole in memory.
func streamRegularFile(src string, destRoot *os.Root, relativePath string, mode os.FileMode) (err error) {
	in, err := os.Open(src) //nolint:gosec // G304: walked entry under caller-supplied source
	if err != nil {
		return fmt.Errorf("open source %q: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	out, err := openStreamDest(destRoot, relativePath, mode)
	if err != nil {
		return fmt.Errorf("create destination %q: %w", relativePath, err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("close destination %q: %w", relativePath, cerr))
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %q to %q: %w", src, relativePath, err)
	}
	if err := destRoot.Chmod(relativePath, mode); err != nil {
		return fmt.Errorf("chmod %q: %w", relativePath, err)
	}
	return nil
}
