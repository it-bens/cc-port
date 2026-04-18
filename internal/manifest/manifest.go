// Package manifest defines the wire DTOs and I/O for the cc-port archive
// metadata.xml file.
package manifest

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"os"
	"time"
)

// Metadata is the root element of the manifest XML file.
type Metadata struct {
	XMLName      xml.Name      `xml:"cc-port"`
	Export       Info          `xml:"export"`
	Placeholders []Placeholder `xml:"placeholders>placeholder"`
}

// Info contains information about the export, including when it was created
// and which categories were included or excluded.
type Info struct {
	Created    time.Time  `xml:"created"`
	Categories []Category `xml:"categories>category"`
}

// Category describes a named category and whether it was included in the export.
type Category struct {
	Name     string `xml:"name,attr"`
	Included bool   `xml:"included,attr"`
}

// Placeholder maps a key to its original value, and optionally carries a
// resolved replacement and a flag indicating whether it is resolvable.
type Placeholder struct {
	Key        string `xml:"key,attr"`
	Original   string `xml:"original,attr"`
	Resolvable *bool  `xml:"resolvable,attr,omitempty"`
	Resolve    string `xml:"resolve,attr,omitempty"`
}

// WriteManifest marshals metadata to indented XML and writes it to path,
// prepending the standard XML declaration header.
func WriteManifest(path string, metadata *Metadata) error {
	data, err := xml.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	content := append([]byte(xml.Header), data...)

	if err := os.WriteFile(path, content, 0644); err != nil { //nolint:gosec // G306: manifest files are user-readable
		return fmt.Errorf("write manifest file: %w", err)
	}

	return nil
}

// ReadManifest reads path and unmarshals the XML content into a Metadata value.
func ReadManifest(path string) (*Metadata, error) {
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

// ReadManifestFromZip opens the ZIP archive at archivePath, locates
// metadata.xml inside it, and unmarshals the content into a Metadata value.
func ReadManifestFromZip(archivePath string) (*Metadata, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}
	defer func() { _ = reader.Close() }()

	for _, file := range reader.File {
		if file.Name != "metadata.xml" {
			continue
		}

		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open metadata.xml in zip: %w", err)
		}
		defer func() { _ = rc.Close() }()

		var metadata Metadata
		if err := xml.NewDecoder(rc).Decode(&metadata); err != nil {
			return nil, fmt.Errorf("unmarshal manifest from zip: %w", err)
		}

		return &metadata, nil
	}

	return nil, fmt.Errorf("metadata.xml not found in zip archive")
}
