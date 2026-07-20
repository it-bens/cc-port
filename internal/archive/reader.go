package archive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

// metadataEntryName is the one archive entry that carries no tool prefix.
const metadataEntryName = "metadata.xml"

// Entry is one archive entry exposed to a tool's Stage method, with the
// leading "<tool>/" path segment already split off by Reader.RawEntries.
// caps is a pointer to the owning Reader's caps (read-only after
// construction) so Entry stays small to copy by value across the package's
// value-receiver methods and call chains.
type Entry struct {
	// Name is the entry's path within its tool's namespace.
	Name      string
	Modified  time.Time
	file      *zip.File
	aggregate *AggregateCounter
	caps      *Caps
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
//
// zip.NewReader itself reads central directory headers until one fails to
// parse, bounded by neither the declared record count nor the declared
// directory size -- only by the archive's actual bytes -- so it is eagerly
// O(entries) before the post-parse MaxEntries check below ever runs. The
// declaredEntryCount check refuses an honestly-oversized archive before
// that eager parse starts; the post-parse check remains as the
// authoritative backstop for an archive whose trailer under-declares its
// true entry count (see README "Not covered").
func OpenReader(src io.ReaderAt, size int64, caps Caps) (*Reader, error) {
	if src == nil {
		return nil, ErrNilSource
	}
	if caps.MaxEntries > 0 {
		if declared, ok := declaredEntryCount(src, size); ok && declared > uint64(caps.MaxEntries) {
			return nil, &EntryCountCapError{Count: clampToInt(declared), Limit: caps.MaxEntries}
		}
	}
	zipReader, err := zip.NewReader(src, size)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	if caps.MaxEntries > 0 && len(zipReader.File) > caps.MaxEntries {
		return nil, &EntryCountCapError{Count: len(zipReader.File), Limit: caps.MaxEntries}
	}
	return &Reader{zipReader: zipReader, caps: caps}, nil
}

const (
	// eocdLen is the fixed-length portion of a ZIP End Of Central Directory
	// record, before its variable-length comment.
	eocdLen = 22
	// eocdMaxCommentLen is the largest comment an EOCD record's 16-bit
	// comment-length field can declare.
	eocdMaxCommentLen = 1<<16 - 1
	// eocdZip64Sentinel is the EOCD entry-count value meaning "see the
	// zip64 End Of Central Directory record instead" -- a plain (non-zip64)
	// archive can never declare more than this many entries at all, so
	// DefaultCaps' 200,000-entry MaxEntries can only ever be exceeded by a
	// zip64 archive; resolving this sentinel is what makes the early check
	// reachable at the production cap, not an optional extra.
	eocdZip64Sentinel = 1<<16 - 1
	// zip64LocatorLen is the fixed length of a zip64 End Of Central
	// Directory Locator, which immediately precedes the EOCD record when
	// the archive declares the zip64 sentinel.
	zip64LocatorLen = 20
	// zip64EndFixedLen is the fixed-length portion of a zip64 End Of
	// Central Directory record, before its extra data.
	zip64EndFixedLen = 56
)

var (
	// eocdSignature opens a ZIP End Of Central Directory record ("PK\x05\x06").
	eocdSignature = []byte{'P', 'K', 0x05, 0x06}
	// zip64LocatorSignature opens a zip64 End Of Central Directory Locator
	// ("PK\x06\x07").
	zip64LocatorSignature = []byte{'P', 'K', 0x06, 0x07}
	// zip64EndSignature opens a zip64 End Of Central Directory record
	// ("PK\x06\x06").
	zip64EndSignature = []byte{'P', 'K', 0x06, 0x06}
)

// declaredEntryCount reads only the fixed-size End Of Central Directory
// trailer at the tail of the archive -- not its central directory -- to
// learn how many entries it declares, following the zip64 locator and end
// record immediately preceding the EOCD when its 16-bit count field is
// saturated. ok is false whenever the trailer (or, for a zip64 archive, the
// locator/end record) can't be located; callers fall back to their own
// authoritative parse in that case. This exists purely to refuse an
// honestly-oversized archive before zip.NewReader's eager, unbounded
// central-directory parse runs -- it is not a replacement for that parse.
func declaredEntryCount(src io.ReaderAt, size int64) (count uint64, ok bool) {
	if size < eocdLen {
		return 0, false
	}
	windowLen := int64(eocdLen + eocdMaxCommentLen)
	if windowLen > size {
		windowLen = size
	}
	window := make([]byte, windowLen)
	if _, err := src.ReadAt(window, size-windowLen); err != nil && !errors.Is(err, io.EOF) {
		return 0, false
	}

	for i := len(window) - eocdLen; i >= 0; i-- {
		if !bytes.Equal(window[i:i+4], eocdSignature) {
			continue
		}
		commentLen := int(binary.LittleEndian.Uint16(window[i+20 : i+22]))
		if i+eocdLen+commentLen > len(window) {
			continue // truncated comment: not a genuine EOCD match, keep scanning
		}
		declared := binary.LittleEndian.Uint16(window[i+10 : i+12])
		if declared != eocdZip64Sentinel {
			return uint64(declared), true
		}
		eocdOffset := size - windowLen + int64(i)
		return zip64DeclaredEntryCount(src, eocdOffset, size)
	}
	return 0, false
}

// zip64DeclaredEntryCount resolves the true entry count for a zip64 archive
// by reading the zip64 locator immediately before eocdOffset, then the
// zip64 End Of Central Directory record it points at. Both are fixed-size
// records read at a known offset -- this is not central-directory parsing,
// just two more bounded reads.
func zip64DeclaredEntryCount(src io.ReaderAt, eocdOffset, size int64) (count uint64, ok bool) {
	locatorOffset := eocdOffset - zip64LocatorLen
	if locatorOffset < 0 {
		return 0, false
	}
	locator := make([]byte, zip64LocatorLen)
	if _, err := src.ReadAt(locator, locatorOffset); err != nil {
		return 0, false
	}
	if !bytes.Equal(locator[0:4], zip64LocatorSignature) {
		return 0, false
	}
	zip64EndOffset := int64(binary.LittleEndian.Uint64(locator[8:16])) //nolint:gosec // G115: bounds-checked below
	if zip64EndOffset < 0 || zip64EndOffset > size-zip64EndFixedLen {
		return 0, false
	}
	zip64End := make([]byte, zip64EndFixedLen)
	if _, err := src.ReadAt(zip64End, zip64EndOffset); err != nil {
		return 0, false
	}
	if !bytes.Equal(zip64End[0:4], zip64EndSignature) {
		return 0, false
	}
	return binary.LittleEndian.Uint64(zip64End[32:40]), true
}

// clampToInt converts n to int, saturating at math.MaxInt for a value that
// would otherwise overflow. Used only for EntryCountCapError's diagnostic
// Count field; the refusal decision itself always compares in uint64.
func clampToInt(n uint64) int {
	if n > math.MaxInt {
		return math.MaxInt
	}
	return int(n)
}

// RawEntries returns every entry except metadata.xml, split into its
// leading tool-name path segment and the tool-relative remainder. An entry
// whose name carries no '/' separator, or an empty leading segment,
// produces ErrMalformedEntryName naming the raw archive path. A
// tool-relative remainder that fails validArchiveEntryName (a dot segment,
// an empty segment, or an absolute path) produces ErrZipSlip here, before
// any consumer routes on the unclean name — the same containment check
// StageSibling repeats as defense in depth once relativePath is known.
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
		if !validArchiveEntryName(rest) {
			return nil, fmt.Errorf("%w: invalid archive entry name %q", ErrZipSlip, file.Name)
		}
		entries = append(entries, RawEntry{
			ToolName: toolName,
			Entry:    Entry{Name: rest, Modified: file.Modified, file: file, caps: &r.caps},
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
	if entry.aggregate == nil {
		return readCloser, capped, nil
	}
	return readCloser, &aggregateCountingReader{inner: capped, counter: entry.aggregate, name: entry.file.Name}, nil
}

type aggregateCountingReader struct {
	inner   io.Reader
	counter *AggregateCounter
	name    string
}

func (r *aggregateCountingReader) Read(p []byte) (int, error) {
	remaining := r.counter.maxAggregateBytes - r.counter.total
	if remaining < 0 {
		return 0, &AggregateCapError{Name: r.name, Bytes: r.counter.total, Limit: r.counter.maxAggregateBytes}
	}
	if maxRead := remaining + 1; int64(len(p)) > maxRead {
		p = p[:maxRead]
	}
	n, readErr := r.inner.Read(p)
	if n == 0 {
		return n, readErr
	}
	if aggregateErr := r.counter.AddEntry(r.name, int64(n)); aggregateErr != nil {
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return 0, errors.Join(readErr, aggregateErr)
		}
		return 0, aggregateErr
	}
	return n, readErr
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
		return nil, fmt.Errorf("read zip entry %q: %w", entry.file.Name, err)
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

// AddEntry records n observed bytes for name and reports the entry that made
// the aggregate staging budget exceed its limit.
func (c *AggregateCounter) AddEntry(name string, n int64) error {
	c.total += n
	if c.total > c.maxAggregateBytes {
		return &AggregateCapError{Name: name, Bytes: c.total, Limit: c.maxAggregateBytes}
	}
	return nil
}
