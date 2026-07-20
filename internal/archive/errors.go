// Package archive implements the cc-port ZIP layout shared by every tool:
// one "<tool>/" namespace per registered tool, entry decompression caps,
// os.Root containment for staged writes, and placeholder substitution.
// Command packages and tool adapters both depend on this package; it
// depends on neither.
package archive

import (
	"errors"
	"fmt"
)

// ErrEntryCapExceeded is returned when an archive entry's uncompressed size
// exceeds the active per-entry cap. Fires from both the declared-size check
// (zip central directory) and the post-decode byte counter.
var ErrEntryCapExceeded = errors.New("archive entry exceeds per-entry size limit")

// ErrAggregateCapExceeded is returned when the running total of decompressed
// bytes across all entries observed so far exceeds the active aggregate cap.
var ErrAggregateCapExceeded = errors.New("archive aggregate decompressed size exceeds limit")

// ErrEntryCountCapExceeded is returned when an archive's central directory
// carries more entries than the active MaxEntries cap.
var ErrEntryCountCapExceeded = errors.New("archive entry count exceeds limit")

// ErrZipSlip is returned when an archive entry's resolved relative path
// would land outside its staging base directory.
var ErrZipSlip = errors.New("staging path escapes containment base")

// ErrStagingFailed is returned when the staging jail itself cannot be
// established: the staging base directory cannot be created, or the
// containing os.Root cannot be opened.
var ErrStagingFailed = errors.New("staging base setup failed")

// ErrMalformedEntryName is returned when a non-metadata.xml archive entry's
// name carries no "<tool>/" leading segment, or an empty one.
var ErrMalformedEntryName = errors.New("archive entry name carries no tool prefix")

// ErrNilSource is returned by OpenReader when src is nil. The message hints
// that the caller's pipeline likely missed MaterializeStage.
var ErrNilSource = errors.New("archive: src is nil; pipeline missing MaterializeStage?")

// EntryCapError names the entry whose declared or observed size tripped
// ErrEntryCapExceeded. Callers inspect it via errors.As.
type EntryCapError struct {
	Name  string
	Bytes uint64
	Limit int64
}

func (e *EntryCapError) Error() string {
	return fmt.Sprintf("%s: %q is %d bytes > limit %d", ErrEntryCapExceeded, e.Name, e.Bytes, e.Limit)
}

func (e *EntryCapError) Unwrap() error { return ErrEntryCapExceeded }

// AggregateCapError identifies the entry whose observed bytes caused the
// archive-wide decompression budget to be exceeded.
type AggregateCapError struct {
	Name  string
	Bytes int64
	Limit int64
}

func (e *AggregateCapError) Error() string {
	return fmt.Sprintf("%s: entry %q made aggregate %d > limit %d", ErrAggregateCapExceeded, e.Name, e.Bytes, e.Limit)
}

func (e *AggregateCapError) Unwrap() error { return ErrAggregateCapExceeded }

// EntryCountCapError names the central directory entry count and limit that
// tripped ErrEntryCountCapExceeded. Callers inspect it via errors.As.
type EntryCountCapError struct {
	Count int
	Limit int
}

func (e *EntryCountCapError) Error() string {
	return fmt.Sprintf("%s: %d entries > limit %d", ErrEntryCountCapExceeded, e.Count, e.Limit)
}

func (e *EntryCountCapError) Unwrap() error { return ErrEntryCountCapExceeded }
