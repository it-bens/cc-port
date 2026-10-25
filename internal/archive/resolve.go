package archive

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// ResolvePlaceholdersStream reads from src and writes to dst, replacing
// each declared placeholder key with its resolution value. Peak memory is
// bounded by the longest placeholder key, not by the source size. Tokens
// never span a read boundary because the reader peeks ahead up to the
// longest declared key before consuming the rest of a token.
//
// Match order is deterministic: resolutions are visited in descending key
// length so a key that is a prefix of another still resolves to the
// longest match. The cc-port {{UPPER_SNAKE}} grammar guarantees tokens are
// self-delimiting, but the deterministic walk keeps tests that pass
// arbitrary keys reliable.
//
// An unmatched `{` is emitted as-is. No byte is silently dropped.
func ResolvePlaceholdersStream(src io.Reader, dst io.Writer, resolutions map[string]string) error {
	type resolution struct {
		tail  []byte
		value string
	}
	entries := make([]resolution, 0, len(resolutions))
	for key, value := range resolutions {
		if tail, isToken := strings.CutPrefix(key, "{"); isToken {
			entries = append(entries, resolution{tail: []byte(tail), value: value})
		}
	}
	if len(entries) == 0 {
		_, err := io.Copy(dst, src)
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return len(entries[i].tail) > len(entries[j].tail)
	})

	reader := bufio.NewReaderSize(src, 64<<10)
	writer := bufio.NewWriterSize(dst, 64<<10)
	for {
		chunk, err := reader.ReadSlice('{')
		text := chunk
		if err == nil {
			text = chunk[:len(chunk)-1]
		}
		// ReadSlice's slice is invalidated by the next reader call, so it is
		// written before the Peek.
		if _, writeErr := writer.Write(text); writeErr != nil {
			return writeErr
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return writer.Flush()
		case err != nil:
			return err
		}
		// Ignoring Peek's error is safe: a short peek at EOF falls through to
		// the prefix check, and a read error returns from a later ReadSlice.
		peek, _ := reader.Peek(len(entries[0].tail))
		matched := false
		for _, entry := range entries {
			if bytes.HasPrefix(peek, entry.tail) {
				if _, writeErr := writer.WriteString(entry.value); writeErr != nil {
					return writeErr
				}
				if _, discardErr := reader.Discard(len(entry.tail)); discardErr != nil {
					return discardErr
				}
				matched = true
				break
			}
		}
		if !matched {
			if writeErr := writer.WriteByte('{'); writeErr != nil {
				return writeErr
			}
		}
	}
}

// ResolveEntryBytes reads and resolves an entry retained in memory, routed
// through the same ResolvePlaceholdersStream + countingWriter primitive the
// streaming staging path uses. Expansion is bounded at Caps.MaxEntryBytes as
// it is written, before the resolved body is fully allocated — a small
// archive whose resolution value is repeated many times cannot pre-allocate
// gigabytes before the cap fires.
func ResolveEntryBytes(entry Entry, resolutions map[string]string) ([]byte, error) {
	readCloser, capped, err := entry.openCapped()
	if err != nil {
		return nil, err
	}
	defer func() { _ = readCloser.Close() }()

	counted := &countingReader{inner: capped}
	var out bytes.Buffer
	bounded := &countingWriter{inner: &out, name: entry.file.Name, limit: entry.caps.MaxEntryBytes}
	if err := ResolvePlaceholdersStream(counted, bounded, resolutions); err != nil {
		return nil, fmt.Errorf("resolve zip entry %q: %w", entry.file.Name, err)
	}
	if err := enforcePostDecodeCap(entry.file.Name, counted.read, entry.caps.MaxEntryBytes); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ValidateResolutions checks that every resolution is a non-empty absolute
// path. Empty values are always rejected: a missing resolution means the
// caller forgot to fill one in.
func ValidateResolutions(resolutions map[string]string) error {
	invalid := make([]string, 0)
	for placeholder, value := range resolutions {
		if value == "" || !filepath.IsAbs(value) {
			invalid = append(invalid, placeholder)
		}
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		return &InvalidResolutionsError{Keys: invalid}
	}
	return nil
}

// InvalidResolutionsError identifies resolution keys whose values are empty or
// not absolute paths. Callers inspect it with errors.As to report malformed
// manifest input without parsing an error string.
type InvalidResolutionsError struct {
	Keys []string
}

func (e *InvalidResolutionsError) Error() string {
	return fmt.Sprintf("resolution values must be non-empty absolute paths for keys: %v", e.Keys)
}
