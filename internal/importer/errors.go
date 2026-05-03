// Package importer error values. Tests assert against these via
// errors.Is / errors.As rather than substring-matching Error() output.
package importer

import (
	"errors"
	"fmt"
)

// ErrEncodedDirCollision is returned by CheckConflict when the encoded
// project directory already exists. The wrapping error message names the
// path; callers test branch identity with errors.Is.
var ErrEncodedDirCollision = errors.New("encoded project directory already exists")

// ErrStatProjectDirectory is returned by CheckConflict when the encoded
// project directory's existence cannot be determined (e.g. permission
// error on an intermediate path component). The wrapping error preserves
// the underlying os error via %w; callers can chain errors.Is against
// both this sentinel and the underlying os error type.
var ErrStatProjectDirectory = errors.New("stat project directory")

// ErrEntryCapExceeded is returned when an archive entry's uncompressed
// size exceeds the per-entry cap (maxZipEntryBytes). The wrapping error
// names the entry and the limit. Fires from both the declared-size check
// (zip central directory) and the post-decode counter.
var ErrEntryCapExceeded = errors.New("archive entry exceeds per-entry size limit")

// ErrAggregateCapExceeded is returned when the sum of decompressed bytes
// across all archive entries exceeds maxArchiveUncompressedBytes. Fires
// from both classifyArchive (pass one) and stageArchiveEntries (pass two);
// pass two re-checks against actual observed bytes, not declared sizes.
var ErrAggregateCapExceeded = errors.New("archive aggregate decompressed size exceeds limit")

// UnknownArchiveEntryError reports an archive entry whose name does not
// match any known prefix. Name carries the offending entry as observed in
// the archive; tests assert on the field via errors.As.
type UnknownArchiveEntryError struct {
	Name string
}

func (e *UnknownArchiveEntryError) Error() string {
	return fmt.Sprintf("unknown archive entry: %q", e.Name)
}
