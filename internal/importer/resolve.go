package importer

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/it-bens/cc-port/internal/manifest"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// projectPathKey is the manifest key cc-port always pre-fills with the import
// target path. Run injects it unconditionally, so it is treated as resolved
// even when the caller did not list it explicitly. Lowercase: callers outside
// internal/importer test membership via IsImplicitKey, not the constant.
const projectPathKey = "{{PROJECT_PATH}}"

// homePathKey is the manifest key cc-port supplies from the import
// machine's os.UserHomeDir(). importer.Run injects it into the
// resolution map via withImplicitAnchors. The IsImplicitKey predicate
// covers it once Task 15 lands.
const homePathKey = "{{HOME}}"

// IsImplicitKey reports whether key is supplied by the import flow itself,
// not by manifest <resolve>, --resolution flags, or interactive prompting.
// The implicit set covers {{PROJECT_PATH}} (supplied from the import target
// argument) and {{HOME}} (supplied from the import machine's
// os.UserHomeDir() via resolveHomeAnchor). Callers refuse user-supplied
// resolutions for these keys and treat them as already-resolved when
// computing unresolved sets. Adding a new implicit key requires only an
// edit here plus a corresponding cmd-layer wiring.
func IsImplicitKey(key string) bool {
	return key == projectPathKey || key == homePathKey
}

// applyResolutions replaces each placeholder token in content with its
// resolved value. Tokens without a mapping are left verbatim — the import
// pre-flight gate in Run is responsible for refusing archives with
// unresolved declared keys before reaching this point, so surviving literals
// here are exclusively ones the manifest explicitly marked Resolvable: false.
//
// Substitution uses plain bytes.ReplaceAll rather than the boundary-aware
// rewrite.ReplacePathInBytes. The token shape `{{KEY}}` is self-delimiting —
// the `}}` suffix is the terminator, and no cc-port token can appear as a
// substring of another (one placeholder cannot be a prefix of another under
// the upper-snake key grammar). Boundary-awareness here would incorrectly
// refuse to substitute when the byte after `}}` happens to be a path
// component (e.g. `{{PROJECT_PATH}}.` in prose), leaving literal tokens on
// disk. The pre-flight gate in Run enforces the "no unresolved tokens
// survive unless marked Resolvable: false" contract so plain replacement is
// safe here.
func applyResolutions(content []byte, resolutions map[string]string) []byte {
	for placeholder, value := range resolutions {
		content = bytes.ReplaceAll(content, []byte(placeholder), []byte(value))
	}
	return content
}

// ResolvePlaceholdersStream reads from src and writes to dst, replacing
// each declared placeholder key with its resolution value. Peak memory is
// bounded by the longest placeholder key, not by the source size. Tokens
// never span a read boundary because the reader peeks ahead up to the
// longest declared key before consuming the byte.
//
// Match order is deterministic: resolutions are visited in descending key
// length so a key that is a prefix of another still resolves to the
// longest match. The cc-port {{UPPER_SNAKE}} grammar guarantees tokens
// are self-delimiting, but the deterministic walk keeps tests that pass
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

// ValidateResolutions checks that every resolution is a non-empty absolute
// path. Empty values are always rejected: the pre-flight gate routes keys
// marked Resolvable: false past Resolutions entirely, so empty values here
// can only mean the caller forgot to fill one in.
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

// ClassifyPlaceholders inspects the placeholder token state of an archive
// about to be imported and reports what is missing or unaccounted for.
//
// Inputs:
//   - bodies: every ZIP entry's content (after metadata.xml has been
//     excluded). Order and length are irrelevant; only substring membership
//     matters for the missing check, and only the union of upper-snake
//     tokens matters for the undeclared check.
//   - declared: the manifest's declared placeholders. Resolvable semantics —
//     nil and *true both mean "must be resolved", *false means "explicitly
//     allowed to remain symbolic".
//   - resolutions: the caller-supplied key → value map that Run will pass
//     to applyResolutions.
//
// Returns two alphabetically sorted slices:
//   - missing: declared keys that are embedded in at least one body, are
//     subject to the resolution contract (Resolvable != *false, not the
//     implicit PROJECT_PATH), and are absent from resolutions.
//   - undeclared: upper-snake `{{KEY}}` tokens that appear in at least one
//     body but are not listed in declared at all.
func ClassifyPlaceholders(
	bodies [][]byte,
	declared []manifest.Placeholder,
	resolutions map[string]string,
) (missing, undeclared []string) {
	declaredByKey := make(map[string]manifest.Placeholder, len(declared))
	for _, placeholder := range declared {
		declaredByKey[placeholder.Key] = placeholder
	}

	for _, placeholder := range declared {
		if placeholder.Resolvable != nil && !*placeholder.Resolvable {
			// Explicitly allowed to remain symbolic.
			continue
		}
		if _, isResolved := resolutions[placeholder.Key]; isResolved {
			continue
		}
		if placeholder.Key == projectPathKey {
			// Run injects PROJECT_PATH unconditionally; treat as resolved
			// even if the caller did not list it explicitly.
			continue
		}
		if !anyBodyContains(bodies, placeholder.Key) {
			continue
		}
		missing = append(missing, placeholder.Key)
	}

	for token := range scanUpperSnakeTokens(bodies) {
		if _, isDeclared := declaredByKey[token]; isDeclared {
			continue
		}
		undeclared = append(undeclared, token)
	}

	sort.Strings(missing)
	sort.Strings(undeclared)
	return missing, undeclared
}

// anyBodyContains reports whether token appears as a literal substring in any
// of bodies.
func anyBodyContains(bodies [][]byte, token string) bool {
	needle := []byte(token)
	for _, body := range bodies {
		if bytes.Contains(body, needle) {
			return true
		}
	}
	return false
}

// scanUpperSnakeTokens returns the set of upper-snake `{{KEY}}` tokens the
// body-byte scanner can see across bodies. The scanner's grammar is narrow
// by design (see rewrite.FindPlaceholderTokens) — this set is only used to
// detect undeclared tokens as a best-effort tamper check, never to drive
// resolution.
func scanUpperSnakeTokens(bodies [][]byte) map[string]struct{} {
	present := make(map[string]struct{})
	for _, body := range bodies {
		for _, token := range rewrite.FindPlaceholderTokens(body) {
			present[token] = struct{}{}
		}
	}
	return present
}

// CheckConflict verifies that a project does not already exist at the target
// path. Returns an error if the encoded project directory exists, or if its
// existence cannot be determined — e.g. a permission error on an intermediate
// component. Returning nil requires a clean "does not exist" answer so the
// "refuse before any write" contract cannot be silently bypassed by a stat
// error that happens to mask a real collision.
func CheckConflict(encodedProjectDir string) error {
	_, err := os.Stat(encodedProjectDir)
	if err == nil {
		return fmt.Errorf("project directory %q: %w", encodedProjectDir, ErrEncodedDirCollision)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w %q: %w", ErrStatProjectDirectory, encodedProjectDir, err)
	}
	return nil
}
