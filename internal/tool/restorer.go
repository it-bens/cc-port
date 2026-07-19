package tool

import (
	"errors"
	"fmt"
	"io"
	"os"

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

// RegisterFile snapshots path's current contents before a caller overwrites
// it in place. Files under siblingBackupThreshold are held as an in-memory
// byte copy; larger files are streamed to a sibling backup file first, so
// restoring a large rewritten file does not hold the whole original in RAM.
func (restorer *Restorer) RegisterFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s for rollback snapshot: %w", path, err)
	}
	if info.Size() < siblingBackupThreshold {
		return restorer.registerInMemory(path, info.Mode())
	}
	return restorer.registerSibling(path, info.Mode())
}

func (restorer *Restorer) registerInMemory(path string, mode os.FileMode) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is caller-supplied, already-validated internal state
	if err != nil {
		return fmt.Errorf("read %s for rollback snapshot: %w", path, err)
	}
	restorer.restores = append(restorer.restores, func() error {
		return rewrite.SafeWriteFile(path, data, mode)
	})
	return nil
}

func (restorer *Restorer) registerSibling(path string, mode os.FileMode) error {
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
		return os.Rename(sibling, path)
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
