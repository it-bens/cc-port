package archive

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
)

// ResolvePlaceholdersStream reads from src and writes to dst, replacing
// each declared placeholder key with its resolution value. Peak memory is
// bounded by the longest placeholder key, not by the source size. Tokens
// never span a read boundary because the reader peeks ahead up to the
// longest declared key before consuming the byte.
//
// Match order is deterministic: resolutions are visited in descending key
// length so a key that is a prefix of another still resolves to the
// longest match. The cc-port {{UPPER_SNAKE}} grammar guarantees tokens are
// self-delimiting, but the deterministic walk keeps tests that pass
// arbitrary keys reliable.
//
// An unmatched `{` is emitted as-is. No byte is silently dropped.
func ResolvePlaceholdersStream(src io.Reader, dst io.Writer, resolutions map[string]string) error {
	if len(resolutions) == 0 {
		_, err := io.Copy(dst, src)
		return err
	}

	orderedKeys := make([]string, 0, len(resolutions))
	longestKey := 0
	for key := range resolutions {
		orderedKeys = append(orderedKeys, key)
		if len(key) > longestKey {
			longestKey = len(key)
		}
	}
	sort.Slice(orderedKeys, func(i, j int) bool {
		return len(orderedKeys[i]) > len(orderedKeys[j])
	})

	reader := bufio.NewReaderSize(src, 64<<10)
	writer := bufio.NewWriterSize(dst, 64<<10)
	for {
		nextByte, err := reader.ReadByte()
		if errors.Is(err, io.EOF) {
			return writer.Flush()
		}
		if err != nil {
			return err
		}
		if nextByte != '{' {
			if writeErr := writer.WriteByte(nextByte); writeErr != nil {
				return writeErr
			}
			continue
		}
		// Potential `{{KEY}}` start. Peek enough to cover the longest key
		// minus the byte we already consumed. Peek may return fewer bytes
		// than requested at EOF; that's fine, the prefix check handles it.
		peek, _ := reader.Peek(longestKey - 1)
		candidate := make([]byte, 0, longestKey)
		candidate = append(candidate, nextByte)
		candidate = append(candidate, peek...)

		matched := false
		for _, key := range orderedKeys {
			if bytes.HasPrefix(candidate, []byte(key)) {
				if _, writeErr := writer.WriteString(resolutions[key]); writeErr != nil {
					return writeErr
				}
				if _, discardErr := reader.Discard(len(key) - 1); discardErr != nil {
					return discardErr
				}
				matched = true
				break
			}
		}
		if !matched {
			if writeErr := writer.WriteByte(nextByte); writeErr != nil {
				return writeErr
			}
		}
	}
}

// ApplyResolutions replaces each placeholder token in content with its
// resolved value. Tokens without a mapping are left verbatim — callers
// refuse archives with unresolved declared keys before reaching this point.
//
// Substitution uses plain bytes.ReplaceAll rather than boundary-aware
// replacement. The token shape `{{KEY}}` is self-delimiting — the `}}`
// suffix is the terminator, and no cc-port token can appear as a substring
// of another under the upper-snake key grammar — so a boundary check would
// incorrectly refuse to substitute when the byte after `}}` happens to be a
// path component (e.g. `{{PROJECT_PATH}}.` in prose).
func ApplyResolutions(content []byte, resolutions map[string]string) []byte {
	for placeholder, value := range resolutions {
		content = bytes.ReplaceAll(content, []byte(placeholder), []byte(value))
	}
	return content
}

// ValidateResolutions checks that every resolution is a non-empty absolute
// path. Empty values are always rejected: a missing resolution means the
// caller forgot to fill one in.
func ValidateResolutions(resolutions map[string]string) error {
	for placeholder, value := range resolutions {
		if value == "" {
			return fmt.Errorf("resolution for %q is empty", placeholder)
		}
		if !filepath.IsAbs(value) {
			return fmt.Errorf("resolution for %q is not an absolute path: %q", placeholder, value)
		}
	}
	return nil
}
