// Package manifest defines the wire DTOs and I/O for the cc-port archive
// metadata.xml file.
package manifest

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
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
func ReadManifestFromZip(src io.ReaderAt, size int64) (*Metadata, error) {
	if src == nil {
		return nil, ErrNilSource
	}
	reader, err := zip.NewReader(src, size)
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}

	for _, file := range reader.File {
		if file.Name != "metadata.xml" {
			continue
		}
		return readManifestEntry(file)
	}
	return nil, fmt.Errorf("metadata.xml not found in zip archive")
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
