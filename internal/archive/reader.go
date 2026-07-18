package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// metadataEntryName is the one archive entry that carries no tool prefix.
const metadataEntryName = "metadata.xml"

// Entry is one archive entry exposed to a tool's Stage method, with the
// leading "<tool>/" path segment already split off by Reader.RawEntries.
type Entry struct {
	// Name is the entry's path within its tool's namespace.
	Name      string
	Modified  time.Time
	file      *zip.File
	aggregate *AggregateCounter
	caps      Caps
}

// RawEntry pairs an Entry with the tool name its archive path declared.
type RawEntry struct {
	ToolName string
	Entry    Entry
}

// Reader wraps a zip.Reader for one archive read, giving every consumer
// (manifest validation, tool-prefix routing, cap enforcement) a single
// shared view of the same central directory.
type Reader struct {
	zipReader *zip.Reader
	caps      Caps
}

// OpenReader opens src as a ZIP archive exposed as random-access bytes.
// Callers materialize their pipeline (file, decrypted tempfile, in-memory
// bytes) before calling; this package never touches paths.
func OpenReader(src io.ReaderAt, size int64, caps Caps) (*Reader, error) {
	if src == nil {
		return nil, ErrNilSource
	}
	zipReader, err := zip.NewReader(src, size)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	return &Reader{zipReader: zipReader, caps: caps}, nil
}

// RawEntries returns every entry except metadata.xml, split into its
// leading tool-name path segment and the tool-relative remainder. An entry
// whose name carries no '/' separator, or an empty leading segment,
// produces ErrMalformedEntryName naming the raw archive path.
func (r *Reader) RawEntries() ([]RawEntry, error) {
	entries := make([]RawEntry, 0, len(r.zipReader.File))
	for _, file := range r.zipReader.File {
		if file.Name == metadataEntryName {
			continue
		}
		toolName, rest, ok := splitToolPrefix(file.Name)
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrMalformedEntryName, file.Name)
		}
		entries = append(entries, RawEntry{
			ToolName: toolName,
			Entry:    Entry{Name: rest, Modified: file.Modified, file: file, caps: r.caps},
		})
	}
	return entries, nil
}

// splitToolPrefix splits name on its first '/' into a non-empty leading
// segment and a non-empty remainder. ok is false when name has no
// separator, an empty leading segment, or nothing after the separator.
func splitToolPrefix(name string) (toolName, rest string, ok bool) {
	index := strings.IndexByte(name, '/')
	if index <= 0 || index == len(name)-1 {
		return "", "", false
	}
	return name[:index], name[index+1:], true
}

// openCapped opens entry's body and wraps it in an io.LimitReader sized one
// byte past its per-entry cap, after rejecting entries whose
// declared UncompressedSize64 already exceeds it. The post-decode counter
// enforced by the caller (see WriteIntoRoot, StageSibling, ReadAll) catches
// archives that misdeclare the size.
func (entry Entry) openCapped() (io.ReadCloser, io.Reader, error) {
	if entry.file.UncompressedSize64 > uint64(entry.caps.MaxEntryBytes) { //nolint:gosec // G115: MaxEntryBytes is positive by construction
		return nil, nil, &EntryCapError{Name: entry.file.Name, Bytes: entry.file.UncompressedSize64, Limit: entry.caps.MaxEntryBytes}
	}
	readCloser, err := entry.file.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("open zip entry %q: %w", entry.file.Name, err)
	}
	capped := io.LimitReader(readCloser, entry.caps.MaxEntryBytes+1)
	return readCloser, capped, nil
}

func enforcePostDecodeCap(name string, bytesRead, limit int64) error {
	if bytesRead > limit {
		return &EntryCapError{Name: name, Bytes: uint64(bytesRead), Limit: limit} //nolint:gosec // G115: bytesRead is non-negative
	}
	return nil
}

// ReadAll reads entry's whole body behind the shared per-entry cap. Used by
// callers that must merge a body in memory (e.g. one history.jsonl append)
// rather than stream it straight to disk.
func (entry Entry) ReadAll() ([]byte, error) {
	readCloser, capped, err := entry.openCapped()
	if err != nil {
		return nil, err
	}
	defer func() { _ = readCloser.Close() }()

	data, err := io.ReadAll(capped)
	if err != nil {
		if aggregateErr := entry.addAggregate(int64(len(data))); aggregateErr != nil {
			return nil, errors.Join(fmt.Errorf("read zip entry %q: %w", entry.file.Name, err), aggregateErr)
		}
		return nil, fmt.Errorf("read zip entry %q: %w", entry.file.Name, err)
	}
	if err := entry.addAggregate(int64(len(data))); err != nil {
		return nil, err
	}
	if err := enforcePostDecodeCap(entry.file.Name, int64(len(data)), entry.caps.MaxEntryBytes); err != nil {
		return nil, err
	}
	return data, nil
}

// WithAggregateCounter returns entry configured to add every decompressed byte
// it reads to counter. The importer creates one counter per staging pass.
func (entry Entry) WithAggregateCounter(counter *AggregateCounter) Entry {
	entry.aggregate = counter
	return entry
}

func (entry Entry) addAggregate(bytesRead int64) error {
	if entry.aggregate == nil {
		return nil
	}
	return entry.aggregate.AddEntry(entry.file.Name, bytesRead)
}

// AggregateCounter tallies uncompressed bytes observed across a sequence of
// entries and refuses once the running total passes its aggregate
// cap. Callers create one per archive read and feed it every entry's
// observed byte count, including bytes read for classification passes.
type AggregateCounter struct {
	total             int64
	maxAggregateBytes int64
}

// NewAggregateCounter returns a counter that refuses once the running
// total of AddEntry calls exceeds maxAggregateBytes.
func NewAggregateCounter(maxAggregateBytes int64) *AggregateCounter {
	return &AggregateCounter{maxAggregateBytes: maxAggregateBytes}
}

// Add records n more observed bytes and returns ErrAggregateCapExceeded
// once the running total exceeds its aggregate cap.
func (c *AggregateCounter) Add(n int64) error {
	return c.AddEntry("archive", n)
}

// AddEntry records n observed bytes for name and reports the entry that made
// the aggregate staging budget exceed its limit.
func (c *AggregateCounter) AddEntry(name string, n int64) error {
	c.total += n
	if c.total > c.maxAggregateBytes {
		return &AggregateCapError{Name: name, Bytes: c.total, Limit: c.maxAggregateBytes}
	}
	return nil
}
