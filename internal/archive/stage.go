package archive

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/it-bens/cc-port/internal/fsutil"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// dirPerm is the mode used for directories archive staging creates. 0o755
// so group/others can traverse project subdirs shared with tooling.
const dirPerm = os.FileMode(0o755)

// stagingSuffix is appended to every final destination to form its temp
// path. Import writes to temp paths first, then atomically promotes them.
// It reuses rewrite.ImportStagingSuffix, cc-port's single source of truth
// for its own artifact naming; the suffix is distinctive enough to survive
// casual filesystem inspection if a crash ever leaves one behind.
const stagingSuffix = rewrite.ImportStagingSuffix

// Staged is one artifact a tool's Stage produced: a temp path that a
// rewrite.SafeRenamePromoter will later rename onto Final.
type Staged struct {
	Temp  string
	Final string
}

// StagedSet accumulates every Staged artifact across every tool during one
// import, so promotion can run as a single all-or-nothing batch spanning
// every selected tool.
type StagedSet struct {
	entries []Staged
}

// Add records one staged artifact.
func (set *StagedSet) Add(staged Staged) {
	set.entries = append(set.entries, staged)
}

// All returns every staged artifact recorded so far, in registration order.
func (set *StagedSet) All() []Staged {
	return set.entries
}

// StagingTempPath returns the sibling temp path used to stage finalPath
// before atomic promotion. The temp is formed inside the symlink-resolved
// parent of finalPath so temp and final always live on the same
// filesystem, which os.Rename requires. Without this, a symlinked parent
// pointing at another volume would place the sibling temp on one side of
// the boundary and the rename target on the other, failing with EXDEV.
func StagingTempPath(finalPath string) (string, error) {
	resolvedParent, err := fsutil.ResolveExistingAncestor(filepath.Dir(finalPath))
	if err != nil {
		return "", fmt.Errorf("resolve staging parent for %q: %w", finalPath, err)
	}
	return filepath.Join(resolvedParent, filepath.Base(finalPath)+stagingSuffix), nil
}

// assertWithinRoot gates relativePath through an os.Root opened on baseDir,
// creating baseDir and any missing intermediate directories first. Rejects
// any relativePath that would resolve outside baseDir (zip-slip) before any
// file write happens.
func assertWithinRoot(baseDir, relativePath string) error {
	cleaned := filepath.Clean(relativePath)
	if filepath.IsAbs(relativePath) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %q under %q", ErrZipSlip, relativePath, baseDir)
	}
	if err := os.MkdirAll(baseDir, dirPerm); err != nil {
		return fmt.Errorf("%w: create %q: %w", ErrStagingFailed, baseDir, err)
	}
	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return fmt.Errorf("%w: open root %q: %w", ErrStagingFailed, baseDir, err)
	}
	defer func() { _ = root.Close() }()

	if dir := filepath.Dir(cleaned); dir != "." {
		if err := root.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("%w: %q under %q: %w", ErrZipSlip, cleaned, baseDir, err)
		}
	}
	return nil
}

// validArchiveEntryName inspects relativePath's raw, uncleaned segments.
// filepath.Clean would collapse a traversal like "memory/../secret" down to
// "secret" before this check ever sees the ".." segment, letting an entry
// name that reads as one category on its raw path clean down to a name that
// disagrees with the routing decision made on that raw path.
func validArchiveEntryName(relativePath string) bool {
	if filepath.IsAbs(relativePath) {
		return false
	}
	for _, segment := range strings.Split(filepath.ToSlash(relativePath), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// applyMtime sets path's atime and mtime to mtime. A zero mtime is a
// no-op — callers pass zip.FileHeader.Modified directly, so entries that
// carry no timestamp leave the staged file at its natural staging-time mtime.
func applyMtime(path string, mtime time.Time) error {
	if mtime.IsZero() {
		return nil
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		return fmt.Errorf("set mtime on %q: %w", path, err)
	}
	return nil
}

// StageSibling streams entry's capped body into a sibling temp path beside
// relativePath resolved under baseDir, so a later os.Rename onto the final
// destination is atomic. resolutions substitutes declared placeholder
// tokens as the body streams; a nil map copies the body verbatim (used for
// opaque bytes such as Claude file-history snapshots). baseDir gates
// relativePath through an os.Root handle before any write, containing
// zip-slip escapes. Returns the Staged record for the caller's StagedSet
// plus the number of bytes written after placeholder expansion.
func StageSibling(
	baseDir, relativePath string, entry Entry, resolutions map[string]string, perm os.FileMode, mtime time.Time,
) (Staged, int64, error) {
	if !validArchiveEntryName(relativePath) {
		return Staged{}, 0, fmt.Errorf("%w: invalid archive entry name %q", ErrZipSlip, relativePath)
	}
	if err := assertWithinRoot(baseDir, relativePath); err != nil {
		return Staged{}, 0, err
	}
	finalPath := filepath.Join(baseDir, filepath.Clean(relativePath))
	tempPath, err := StagingTempPath(finalPath)
	if err != nil {
		return Staged{}, 0, err
	}
	// Record the temp before any call that may create it on disk, so the
	// error paths below return a record the caller can still clean up.
	staged := Staged{Temp: tempPath, Final: finalPath}

	bytesRead, err := streamToPath(tempPath, entry, resolutions, perm)
	if err != nil {
		return staged, bytesRead, err
	}
	if err := applyMtime(tempPath, mtime); err != nil {
		return staged, bytesRead, err
	}
	return staged, bytesRead, nil
}

// streamToPath streams entry's capped body into path (creating parent
// directories as needed), resolving placeholder tokens via resolutions
// (nil = verbatim copy). Returns the number of bytes written after placeholder
// expansion.
func streamToPath(path string, entry Entry, resolutions map[string]string, perm os.FileMode) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return 0, fmt.Errorf("create directories for %q: %w", path, err)
	}
	//nolint:gosec // G304: path constructed from resolved, containment-checked staging base
	writer, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return 0, fmt.Errorf("create staging temp %q: %w", path, err)
	}
	defer func() { _ = writer.Close() }()

	bytesRead, err := streamEntry(entry, writer, resolutions)
	if err != nil {
		return bytesRead, err
	}
	if err := writer.Close(); err != nil {
		return bytesRead, fmt.Errorf("close staging temp %q: %w", path, err)
	}
	return bytesRead, nil
}

// streamEntry drives the common open + cap + resolve pipeline shared by
// every staging path. resolutions == nil streams the body verbatim.
func streamEntry(entry Entry, writer *os.File, resolutions map[string]string) (int64, error) {
	readCloser, capped, err := entry.openCapped()
	if err != nil {
		return 0, err
	}
	defer func() { _ = readCloser.Close() }()

	if resolutions == nil {
		bytesRead, err := io.Copy(writer, capped)
		if err != nil {
			return bytesRead, fmt.Errorf("stream zip entry %q: %w", entry.file.Name, err)
		}
		if err := enforcePostDecodeCap(entry.file.Name, bytesRead, entry.caps.MaxEntryBytes); err != nil {
			return bytesRead, err
		}
		return bytesRead, nil
	}

	counted := &countingReader{inner: capped}
	expanded := &countingWriter{inner: writer, name: entry.file.Name, limit: entry.caps.MaxEntryBytes}
	if err := ResolvePlaceholdersStream(counted, expanded, resolutions); err != nil {
		return expanded.bytesWritten, fmt.Errorf("resolve zip entry %q: %w", entry.file.Name, err)
	}
	if err := enforcePostDecodeCap(entry.file.Name, counted.read, entry.caps.MaxEntryBytes); err != nil {
		return expanded.bytesWritten, err
	}
	return expanded.bytesWritten, nil
}

type countingReader struct {
	inner io.Reader
	read  int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	r.read += int64(n)
	return n, err
}

// countingWriter bounds bytes emitted after placeholder expansion. The source
// reader remains capped separately because expansion and decompression are
// independent resource risks.
type countingWriter struct {
	inner        io.Writer
	name         string
	limit        int64
	bytesWritten int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	if w.bytesWritten+int64(len(p)) > w.limit {
		return 0, &EntryCapError{Name: w.name, Bytes: uint64(w.bytesWritten + int64(len(p))), Limit: w.limit} //nolint:gosec // byte counts are non-negative
	}
	n, err := w.inner.Write(p)
	w.bytesWritten += int64(n)
	return n, err
}
