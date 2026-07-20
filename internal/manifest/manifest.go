// Package manifest defines the wire DTOs and I/O for the cc-port archive
// metadata.xml file.
package manifest

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/it-bens/cc-port/internal/rewrite"
)

// Metadata is the root element of the manifest XML file. One Tool block
// exists per tool the export touched; a tool that did not know the project
// still writes an empty block (per-tool categories all excluded, no
// placeholders) rather than omitting itself.
type Metadata struct {
	XMLName      xml.Name  `xml:"cc-port"`
	Created      time.Time `xml:"created"`
	Tools        []Tool    `xml:"tool"`
	SyncPushedBy string    `xml:"sync-pushed-by,omitempty"`
	SyncPushedAt string    `xml:"sync-pushed-at,omitempty"` // RFC3339
}

// Tool is one tool's block inside metadata.xml: its category selection and
// its placeholder set. Placeholder keys are scoped to this tool; the same
// key text in two different tools' blocks resolves independently.
type Tool struct {
	Name         string        `xml:"name,attr"`
	Categories   []Category    `xml:"categories>category"`
	Placeholders []Placeholder `xml:"placeholders>placeholder"`
}

// Category describes a named category and whether it was included in the export.
type Category struct {
	Name     string `xml:"name,attr"`
	Included bool   `xml:"included,attr"`
}

// Placeholder maps a key to its original value, and optionally carries a
// resolved replacement.
type Placeholder struct {
	Key      string `xml:"key,attr"`
	Original string `xml:"original,attr"`
	Resolve  string `xml:"resolve,attr,omitempty"`
}

// ToolBlock returns the named tool's block and true, or a zero Tool and
// false when metadata carries no block for that name.
func (metadata *Metadata) ToolBlock(name string) (Tool, bool) {
	for _, block := range metadata.Tools {
		if block.Name == name {
			return block, true
		}
	}
	return Tool{}, false
}

// maxManifestBytes caps the size of metadata.xml when read from a path or
// a ZIP entry. Real manifests are a few KiB; 4 MiB is generous headroom
// for future placeholder growth and stops decompression-bomb payloads cold.
const maxManifestBytes = 4 << 20

// ErrManifestFileTooLarge is returned by ReadManifest when metadata.xml on disk
// exceeds maxManifestBytes. The wrapping message names the path and the limit;
// callers discriminate via errors.Is.
var ErrManifestFileTooLarge = errors.New("manifest file too large")

// ErrManifestEntryTooLarge is returned by ReadManifestFromZip when the
// metadata.xml zip entry's decoded size exceeds maxManifestBytes. The wrapping
// message names the entry and the limit; callers discriminate via errors.Is.
var ErrManifestEntryTooLarge = errors.New("manifest entry too large")

// ErrManifestArchiveTooManyEntries is returned by ReadManifestFromZip when
// the archive's central directory carries more entries than the active
// maxEntries cap. Mirrors archive.EntryCountCapError: this package cannot
// import internal/archive (archive already imports manifest, for
// WriteMetadata), so it carries its own instance of the same shape.
var ErrManifestArchiveTooManyEntries = errors.New("manifest: archive entry count exceeds limit")

// EntryCountCapError names the declared or observed entry count and limit
// that tripped ErrManifestArchiveTooManyEntries. Callers inspect it via
// errors.As. Named to match archive.EntryCountCapError's shape; the
// package qualifier (manifest.EntryCountCapError) disambiguates the two.
type EntryCountCapError struct {
	Count int
	Limit int
}

func (e *EntryCountCapError) Error() string {
	return fmt.Sprintf("%s: %d entries > limit %d", ErrManifestArchiveTooManyEntries, e.Count, e.Limit)
}

func (e *EntryCountCapError) Unwrap() error { return ErrManifestArchiveTooManyEntries }

// maxPlaceholderKeyBytes bounds a declared placeholder key so the streaming
// resolver's fixed peek window can always match it. Real keys are tens of
// bytes (e.g. {{CODEX_PROJECT_PATH}}); 4 KiB is generous headroom, well
// under the resolver's 64 KiB read window.
const maxPlaceholderKeyBytes = 4 << 10

// ErrPlaceholderKeyTooLong is returned when a manifest declares a
// placeholder key longer than maxPlaceholderKeyBytes. The streaming
// resolver peeks only up to its reader's fixed window to find a match; a
// key longer than that window can never match and would otherwise pass
// through unsubstituted instead of tripping the closed-placeholder refusal
// import promises. The wrapping message names the tool and key length;
// callers discriminate via errors.Is.
var ErrPlaceholderKeyTooLong = errors.New("manifest declares a placeholder key over the length limit")

// ErrPlaceholderKeyMalformed is returned when a manifest declares a
// placeholder key that is not "{{" + non-empty inner segment + "}}". The
// streaming resolver anchors matching on a leading '{' byte, so a key in
// any other shape can never match and would otherwise pass through a body
// unsubstituted instead of tripping the closed-placeholder refusal import
// promises. The wrapping message names the tool and the offending key;
// callers discriminate via errors.Is.
var ErrPlaceholderKeyMalformed = errors.New("manifest declares a placeholder key with invalid grammar")

// ErrNilSource is returned by ReadManifestFromZip when src is nil. The message
// hints that the caller's pipeline likely missed MaterializeStage. Mirrors
// importer.ErrSourceNil.
var ErrNilSource = errors.New("manifest: src is nil; pipeline missing MaterializeStage?")

// DuplicateToolError identifies a manifest carrying more than one block for a
// tool. Refusing it keeps ToolBlock and importer routing from selecting
// different blocks for the same tool.
type DuplicateToolError struct {
	Tool string
}

func (e *DuplicateToolError) Error() string {
	return fmt.Sprintf("manifest contains duplicate tool block %q", e.Tool)
}

// WriteManifest marshals metadata to indented XML and writes it to path.
func WriteManifest(path string, metadata *Metadata) error {
	data, err := xml.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	content := append([]byte(xml.Header), data...)

	if err := rewrite.SafeWriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write manifest file: %w", err)
	}

	return nil
}

// ReadManifest reads path and unmarshals the XML content into a Metadata value.
// Rejects files exceeding maxManifestBytes before allocating.
func ReadManifest(path string) (*Metadata, error) {
	file, err := os.Open(path) //nolint:gosec // G304: caller-supplied manifest path
	if err != nil {
		return nil, fmt.Errorf("open manifest file: %w", err)
	}
	defer func() { _ = file.Close() }()

	limited := io.LimitReader(file, int64(maxManifestBytes)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read manifest file: %w", err)
	}
	if int64(len(data)) > int64(maxManifestBytes) {
		return nil, fmt.Errorf("%w: %q exceeds the %d-byte limit", ErrManifestFileTooLarge, path, maxManifestBytes)
	}

	var metadata Metadata
	if err := xml.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}
	if err := validateTools(&metadata); err != nil {
		return nil, err
	}
	if err := validatePlaceholderKeys(&metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

// ReadManifestFromZip parses metadata.xml from a ZIP exposed as random-access
// bytes. Callers open the source (file, decrypted tempfile, in-memory bytes)
// and pass it through; the manifest package never touches paths.
// Rejects entries whose decoded size exceeds maxManifestBytes.
//
// maxEntries bounds the archive's declared entry count the same way
// archive.Caps.MaxEntries bounds archive.OpenReader; zero disables the
// check. An archive whose End Of Central Directory record declares more
// entries than maxEntries is refused before zip.NewReader's eager,
// unbounded central-directory parse runs; the post-parse count below is the
// backstop for a trailer that under-declares the true count.
func ReadManifestFromZip(src io.ReaderAt, size int64, maxEntries int) (*Metadata, error) {
	if src == nil {
		return nil, ErrNilSource
	}
	if maxEntries > 0 {
		if declared, ok := declaredEntryCount(src, size); ok && declared > uint64(maxEntries) {
			return nil, &EntryCountCapError{Count: clampToInt(declared), Limit: maxEntries}
		}
	}
	reader, err := zip.NewReader(src, size)
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}
	if maxEntries > 0 && len(reader.File) > maxEntries {
		return nil, &EntryCountCapError{Count: len(reader.File), Limit: maxEntries}
	}

	for _, file := range reader.File {
		if file.Name != "metadata.xml" {
			continue
		}
		return readManifestEntry(file)
	}
	return nil, fmt.Errorf("metadata.xml not found in zip archive")
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
	// archive can never declare more than this many entries at all, so a
	// caller passing a maxEntries above this range can only ever be
	// exceeded by a zip64 archive; resolving this sentinel is what makes
	// the early check reachable at such a cap, not an optional extra.
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
// Duplicated from internal/archive's identical helper: this package cannot
// import that one (see ErrManifestArchiveTooManyEntries).
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

// readManifestEntry reads metadata.xml from a single zip entry, enforces the
// size cap, and unmarshals into Metadata. Scoped to its own function so
// deferred rc.Close() fires once per entry, not once per ReadManifestFromZip call.
func readManifestEntry(file *zip.File) (*Metadata, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open metadata.xml in zip: %w", err)
	}
	defer func() { _ = rc.Close() }()

	// Read at most maxManifestBytes+1 so we can distinguish an
	// exactly-at-limit legitimate manifest from an over-limit one.
	limited := io.LimitReader(rc, int64(maxManifestBytes)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read metadata.xml from zip: %w", err)
	}
	if int64(len(data)) > int64(maxManifestBytes) {
		return nil, fmt.Errorf("%w: %q exceeds the %d-byte limit", ErrManifestEntryTooLarge, file.Name, maxManifestBytes)
	}

	var metadata Metadata
	if err := xml.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("unmarshal manifest from zip: %w", err)
	}
	if err := validateTools(&metadata); err != nil {
		return nil, err
	}
	if err := validatePlaceholderKeys(&metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func validateTools(metadata *Metadata) error {
	seen := make(map[string]struct{}, len(metadata.Tools))
	for _, block := range metadata.Tools {
		if _, exists := seen[block.Name]; exists {
			return &DuplicateToolError{Tool: block.Name}
		}
		seen[block.Name] = struct{}{}
	}
	return nil
}

// validatePlaceholderKeys refuses any tool block whose declared placeholder
// key exceeds maxPlaceholderKeyBytes or is not "{{...}}"-shaped, before the
// key ever reaches archive.ResolvePlaceholdersStream. Both checks guarantee
// every key the resolver sees is one it can structurally match: the length
// cap keeps a key inside the resolver's fixed peek window, and the grammar
// check keeps it anchored on the '{' byte the resolver's matching starts
// from. A key an untrusted archive declares in some other shape must be
// refused here, loudly, rather than silently left unsubstituted in a body.
func validatePlaceholderKeys(metadata *Metadata) error {
	for _, block := range metadata.Tools {
		for _, placeholder := range block.Placeholders {
			if len(placeholder.Key) > maxPlaceholderKeyBytes {
				return fmt.Errorf("%w: tool %q key is %d bytes > limit %d",
					ErrPlaceholderKeyTooLong, block.Name, len(placeholder.Key), maxPlaceholderKeyBytes)
			}
			if !isWellFormedPlaceholderKey(placeholder.Key) {
				return fmt.Errorf("%w: tool %q key %q",
					ErrPlaceholderKeyMalformed, block.Name, placeholder.Key)
			}
		}
	}
	return nil
}

// isWellFormedPlaceholderKey reports whether key is "{{" followed by a
// non-empty inner segment followed by "}}" — the only shape
// archive.ResolvePlaceholdersStream's '{'-anchored matching can ever
// resolve.
func isWellFormedPlaceholderKey(key string) bool {
	const prefix, suffix = "{{", "}}"
	return strings.HasPrefix(key, prefix) && strings.HasSuffix(key, suffix) && len(key) > len(prefix)+len(suffix)
}
