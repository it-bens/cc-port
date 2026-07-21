package archive

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// WrittenEntry names one file a Sink wrote, relative to the tool's own
// namespace (the "<tool>/" prefix is added by the Sink and is not part of
// Name).
type WrittenEntry struct {
	Name string
	Size int64
}

// Sink is the per-tool write surface an Exporter streams entries into. The
// generic export command constructs one Sink per selected tool, bound to
// the shared zip.Writer and that tool's own placeholder set, so every entry
// a tool writes automatically carries its "<tool>/" prefix and its own
// anonymization.
type Sink struct {
	writer       *zip.Writer
	prefix       string
	placeholders []manifest.Placeholder
}

// NewSink returns a Sink that writes entries under "<toolName>/" into
// writer, anonymizing bodies with placeholders.
func NewSink(writer *zip.Writer, toolName string, placeholders []manifest.Placeholder) *Sink {
	return &Sink{writer: writer, prefix: toolName + "/", placeholders: placeholders}
}

// ApplyPlaceholders substitutes every configured placeholder's Original
// path with its Key token in data. Placeholders are applied longest
// Original first so a shorter placeholder cannot consume a legitimate
// prefix of a longer one that ends at a real '/' boundary (e.g.
// "/Users/x" -> "{{HOME}}" running before "/Users/x/project" ->
// "{{PROJECT_PATH}}" would leave "{{HOME}}/project" where
// "{{PROJECT_PATH}}" was intended).
func (sink *Sink) ApplyPlaceholders(data []byte) []byte {
	return applyPlaceholders(data, sink.placeholders)
}

func applyPlaceholders(data []byte, placeholders []manifest.Placeholder) []byte {
	sorted := make([]manifest.Placeholder, len(placeholders))
	copy(sorted, placeholders)
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].Original) > len(sorted[j].Original)
	})
	for _, placeholder := range sorted {
		data, _ = rewrite.ReplacePathInBytes(data, placeholder.Original, placeholder.Key)
	}
	return data
}

func (sink *Sink) createEntry(name string, mtime time.Time) (io.Writer, error) {
	fullName := sink.prefix + name
	if mtime.IsZero() {
		writer, err := sink.writer.Create(fullName)
		if err != nil {
			return nil, fmt.Errorf("create zip entry %s: %w", fullName, err)
		}
		return writer, nil
	}
	header := &zip.FileHeader{Name: fullName, Method: zip.Deflate, Modified: mtime}
	writer, err := sink.writer.CreateHeader(header)
	if err != nil {
		return nil, fmt.Errorf("create zip entry %s: %w", fullName, err)
	}
	return writer, nil
}

// WriteBytes anonymizes data via ApplyPlaceholders, then writes it as a new
// entry at name (a path relative to the tool's own namespace).
func (sink *Sink) WriteBytes(name string, data []byte, mtime time.Time) (WrittenEntry, error) {
	writer, err := sink.createEntry(name, mtime)
	if err != nil {
		return WrittenEntry{}, err
	}
	anonymized := sink.ApplyPlaceholders(data)
	if _, err := writer.Write(anonymized); err != nil {
		return WrittenEntry{}, fmt.Errorf("write zip entry %s: %w", name, err)
	}
	return WrittenEntry{Name: name, Size: int64(len(anonymized))}, nil
}

// WriteVerbatim streams src into a new entry at name with no placeholder
// resolution. Suited to opaque bytes (e.g. Claude file-history snapshots)
// where any byte-level rewrite would violate an opacity contract.
func (sink *Sink) WriteVerbatim(ctx context.Context, name string, src io.Reader, mtime time.Time) (WrittenEntry, error) {
	writer, err := sink.createEntry(name, mtime)
	if err != nil {
		return WrittenEntry{}, err
	}
	var written int64
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return WrittenEntry{}, err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			if _, writeErr := writer.Write(buffer[:n]); writeErr != nil {
				return WrittenEntry{}, fmt.Errorf("write zip entry %s: %w", name, writeErr)
			}
			written += int64(n)
		}
		if errors.Is(readErr, io.EOF) {
			return WrittenEntry{Name: name, Size: written}, nil
		}
		if readErr != nil {
			return WrittenEntry{}, fmt.Errorf("read source for %s: %w", name, readErr)
		}
	}
}

// WriteJSONL streams src line by line through lineTransform into a new
// entry at name. The original line terminator ('\n' or its absence) is
// preserved so the archive entry is byte-identical to what a whole-file
// transform would produce. ctx is checked at each line boundary. A
// lineTransform returning nil drops the line and its terminator entirely; a
// nil lineTransform copies every line verbatim. maxLineBytes caps each line
// including its terminator: a line whose body plus terminator reaches
// maxLineBytes is rejected with bufio.ErrTooLong before the whole line is
// buffered, so a body has one byte of headroom under '\n' and two under '\r\n'.
func (sink *Sink) WriteJSONL(
	ctx context.Context, name string, src io.Reader, maxLineBytes int64, lineTransform func([]byte) []byte, mtime time.Time,
) (WrittenEntry, error) {
	if maxLineBytes <= 0 {
		return WrittenEntry{}, fmt.Errorf("JSONL line limit must be positive: %d", maxLineBytes)
	}
	writer, err := sink.createEntry(name, mtime)
	if err != nil {
		return WrittenEntry{}, err
	}
	initialBufSize := int64(64 << 10)
	if maxLineBytes < initialBufSize {
		initialBufSize = maxLineBytes
	}
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, int(initialBufSize)), int(maxLineBytes))
	scanner.Split(scanJSONLTokens)
	var written int64
	lineNumber := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return WrittenEntry{}, err
		}
		line := scanner.Bytes()
		body, terminator := splitJSONLTerminator(line)
		lineNumber++
		out := body
		if lineTransform != nil {
			out = lineTransform(body)
		}
		if out != nil {
			if _, err := writer.Write(out); err != nil {
				return WrittenEntry{}, fmt.Errorf("write zip entry %s: %w", name, err)
			}
			written += int64(len(out))
			if len(terminator) > 0 {
				if _, err := writer.Write(terminator); err != nil {
					return WrittenEntry{}, fmt.Errorf("write zip entry %s: %w", name, err)
				}
				written += int64(len(terminator))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return WrittenEntry{}, fmt.Errorf("%s line %d must be shorter than %d bytes: %w", name, lineNumber+1, maxLineBytes, err)
	}
	return WrittenEntry{Name: name, Size: written}, nil
}

func scanJSONLTokens(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func splitJSONLTerminator(line []byte) (body, terminator []byte) {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		if len(line) > 1 && line[len(line)-2] == '\r' {
			return line[:len(line)-2], line[len(line)-2:]
		}
		return line[:len(line)-1], line[len(line)-1:]
	}
	return line, nil
}

// WriteMetadata marshals metadata to indented XML and writes it as
// "metadata.xml" at the archive root, outside any tool's namespace.
func WriteMetadata(writer *zip.Writer, metadata *manifest.Metadata) (WrittenEntry, error) {
	body, err := xml.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return WrittenEntry{}, fmt.Errorf("marshal metadata XML: %w", err)
	}
	data := append([]byte(xml.Header), body...)
	entryWriter, err := writer.Create(metadataEntryName)
	if err != nil {
		return WrittenEntry{}, fmt.Errorf("create zip entry %s: %w", metadataEntryName, err)
	}
	if _, err := entryWriter.Write(data); err != nil {
		return WrittenEntry{}, fmt.Errorf("write zip entry %s: %w", metadataEntryName, err)
	}
	return WrittenEntry{Name: metadataEntryName, Size: int64(len(data))}, nil
}
