package tool

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/it-bens/cc-port/internal/rewrite"
)

// siblingBackupThreshold is the file size above which RegisterFile routes
// the rollback snapshot through a streamed sibling backup file rather than
// an in-memory byte copy, so restoring a large rewritten file does not hold
// the whole original in RAM.
const siblingBackupThreshold = 1 << 20 // 1 MiB

// siblingSuffix names the sibling rollback file RegisterFile writes next to
// any tracked target above siblingBackupThreshold. It reuses
// rewrite.RollbackSuffix, cc-port's single source of truth for its own
// rollback-artifact naming.
const siblingSuffix = rewrite.RollbackSuffix

// Restorer collects rollback state for one Surface Apply pass. File
// surfaces call RegisterFile before overwriting a path in place; a future
// non-file surface (e.g. a SQL transaction) calls RegisterUndo with its own
// rollback callback. Restore reverses every registration in reverse
// registration order, joining any errors; Cleanup discards backing state
// once the caller's operation has fully succeeded.
type Restorer struct {
	// restores holds one closure per registration, in registration order;
	// Restore walks it in reverse so the most recent change is undone first.
	restores []func() error
	// cleanups holds one closure per registration that created on-disk
	// backing state (a sibling backup file); most registrations have none.
	cleanups []func()
}

// NewRestorer returns a Restorer ready to accept registrations.
func NewRestorer() *Restorer {
	return &Restorer{}
}

// RegisterFile snapshots path's current contents, mode, and modification
// time before a caller overwrites it in place. Files under
// siblingBackupThreshold are held as an in-memory byte copy; larger files
// are streamed to a sibling backup file first, so restoring a large
// rewritten file does not hold the whole original in RAM. Restoring through
// either path reapplies the captured modification time, so a rollback puts
// back the exact pre-image rather than a copy time-stamped at restore time.
func (restorer *Restorer) RegisterFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s for rollback snapshot: %w", path, err)
	}
	if info.Size() < siblingBackupThreshold {
		return restorer.registerInMemory(path, info.Mode(), info.ModTime())
	}
	return restorer.registerSibling(path, info.Mode(), info.ModTime())
}

func (restorer *Restorer) registerInMemory(path string, mode os.FileMode, modTime time.Time) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is caller-supplied, already-validated internal state
	if err != nil {
		return fmt.Errorf("read %s for rollback snapshot: %w", path, err)
	}
	restorer.restores = append(restorer.restores, func() error {
		if err := rewrite.SafeWriteFile(path, data, mode); err != nil {
			return err
		}
		// SafeWriteFile promotes through a fresh temp file, so the restored
		// path's mtime is the restore time, not the pre-mutation time this
		// snapshot captured; Chtimes puts the original back. Go's os.FileInfo
		// carries no portable atime, so the captured mtime stands in for both.
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			return fmt.Errorf("restore mtime %s: %w", path, err)
		}
		return nil
	})
	return nil
}

func (restorer *Restorer) registerSibling(path string, mode os.FileMode, modTime time.Time) error {
	source, err := os.Open(path) //nolint:gosec // G304: path is caller-supplied, already-validated internal state
	if err != nil {
		return fmt.Errorf("open %s for rollback snapshot: %w", path, err)
	}
	defer func() { _ = source.Close() }()

	sibling := path + siblingSuffix
	//nolint:gosec // G304: sibling path derived from caller-supplied internal state
	destination, err := os.OpenFile(sibling, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create rollback snapshot %s: %w", sibling, err)
	}
	if _, err := io.Copy(destination, source); err != nil {
		_ = destination.Close()
		_ = os.Remove(sibling)
		return fmt.Errorf("copy %s to rollback snapshot %s: %w", path, sibling, err)
	}
	if err := destination.Close(); err != nil {
		_ = os.Remove(sibling)
		return fmt.Errorf("close rollback snapshot %s: %w", sibling, err)
	}

	restorer.restores = append(restorer.restores, func() error {
		if err := os.Rename(sibling, path); err != nil {
			return err
		}
		// The sibling's own mtime is when RegisterFile wrote the backup, not
		// when the original content was last modified; Chtimes restores the
		// captured pre-mutation time the rename alone cannot carry.
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			return fmt.Errorf("restore mtime %s: %w", path, err)
		}
		return nil
	})
	restorer.cleanups = append(restorer.cleanups, func() {
		_ = os.Remove(sibling)
	})
	return nil
}

// RegisterUndo records fn to run during Restore, in the same reverse-order
// sequence as every RegisterFile snapshot. Used by surfaces whose rollback
// is not a file rewrite.
func (restorer *Restorer) RegisterUndo(fn func() error) {
	restorer.restores = append(restorer.restores, fn)
}

// Restore reverses every registration in reverse registration order,
// joining any errors encountered along the way.
func (restorer *Restorer) Restore() error {
	var errs []error
	for index := len(restorer.restores) - 1; index >= 0; index-- {
		if err := restorer.restores[index](); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Cleanup removes any sibling backup files RegisterFile created. Callers
// invoke it once their operation has fully succeeded and no Restore will follow.
func (restorer *Restorer) Cleanup() {
	for _, cleanup := range restorer.cleanups {
		cleanup()
	}
}
