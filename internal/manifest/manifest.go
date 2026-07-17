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
	"time"
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

// ErrNilSource is returned by ReadManifestFromZip when src is nil. The message
// hints that the caller's pipeline likely missed MaterializeStage. Mirrors
// importer.ErrSourceNil.
var ErrNilSource = errors.New("manifest: src is nil; pipeline missing MaterializeStage?")

// WriteManifest marshals metadata to indented XML and writes it to path.
func WriteManifest(path string, metadata *Metadata) error {
	data, err := xml.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	content := append([]byte(xml.Header), data...)

	if err := os.WriteFile(path, content, 0o644); err != nil { //nolint:gosec // G306: manifest files are user-readable
		return fmt.Errorf("write manifest file: %w", err)
	}

	return nil
}

// ReadManifest reads path and unmarshals the XML content into a Metadata value.
// Rejects files exceeding maxManifestBytes before allocating.
func ReadManifest(path string) (*Metadata, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat manifest file: %w", err)
	}
	if info.Size() > int64(maxManifestBytes) {
		return nil, fmt.Errorf("%w: %q exceeds the %d-byte limit", ErrManifestFileTooLarge, path, maxManifestBytes)
	}

	data, err := os.ReadFile(path) //nolint:gosec // G304: caller-supplied manifest path
	if err != nil {
		return nil, fmt.Errorf("read manifest file: %w", err)
	}

	var metadata Metadata
	if err := xml.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
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
	return &metadata, nil
}
