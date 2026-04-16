package importer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/it-bens/cc-port/internal/export"
	"github.com/it-bens/cc-port/internal/rewrite"
)

// projectPathKey is the manifest key cc-port always pre-fills with the import
// target path. It is treated as resolved even when the caller did not list it
// explicitly in Resolutions, because importer.Run injects it unconditionally.
const projectPathKey = "{{PROJECT_PATH}}"

// ResolvePlaceholders replaces each placeholder token in content with its
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
func ResolvePlaceholders(content []byte, resolutions map[string]string) []byte {
	for placeholder, value := range resolutions {
		content = bytes.ReplaceAll(content, []byte(placeholder), []byte(value))
	}
	return content
}

// ValidateResolutions checks that every resolution is a non-empty absolute
// path. Empty values are always rejected — the pre-flight gate routes keys
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
// about to be imported and reports what is missing or unaccounted for. It is
// the closed-contract gate: before any write touches disk, the importer uses
// this to refuse archives that would otherwise leave literal `{{KEY}}`
// strings on disk or would silently substitute keys the archive never
// declared.
//
// Inputs:
//   - bodies: every ZIP entry's content (after metadata.xml has been
//     excluded). Order and length are irrelevant; only the union of
//     placeholder tokens matters.
//   - declared: the manifest's declared placeholders. Resolvable semantics —
//     nil and *true both mean "must be resolved", *false means "explicitly
//     allowed to remain symbolic".
//   - resolutions: the caller-supplied key → value map that Run will pass
//     to ResolvePlaceholders.
//
// Returns two alphabetically sorted slices:
//   - missing: keys that appear in at least one body and are either (a)
//     declared with Resolvable != *false OR (b) the implicit PROJECT_PATH,
//     but are absent from resolutions.
//   - undeclared: keys that appear in at least one body but are not listed
//     in declared at all. These are archive-contract violations — the
//     archive writer knew about a key it didn't publish in metadata.xml.
//
// A declared key that never appears in any body is fine: the archive may
// legitimately include "metadata about paths we considered but did not
// embed". That case is not flagged.
func ClassifyPlaceholders(
	bodies [][]byte,
	declared []export.Placeholder,
	resolutions map[string]string,
) (missing, undeclared []string) {
	present := collectPresentTokens(bodies)

	declaredByKey := make(map[string]export.Placeholder, len(declared))
	for _, placeholder := range declared {
		declaredByKey[placeholder.Key] = placeholder
	}

	for token := range present {
		placeholder, isDeclared := declaredByKey[token]
		if !isDeclared {
			undeclared = append(undeclared, token)
			continue
		}
		if placeholder.Resolvable != nil && !*placeholder.Resolvable {
			// Explicitly allowed to remain symbolic.
			continue
		}
		if _, isResolved := resolutions[token]; isResolved {
			continue
		}
		if token == projectPathKey {
			// Run injects PROJECT_PATH unconditionally; treat as resolved
			// even if the caller did not list it explicitly.
			continue
		}
		missing = append(missing, token)
	}

	sort.Strings(missing)
	sort.Strings(undeclared)
	return missing, undeclared
}

// collectPresentTokens returns the set of placeholder tokens found in any of
// the given bodies.
func collectPresentTokens(bodies [][]byte) map[string]struct{} {
	present := make(map[string]struct{})
	for _, body := range bodies {
		for _, token := range rewrite.FindPlaceholderTokens(body) {
			present[token] = struct{}{}
		}
	}
	return present
}

// CheckConflict verifies that a project does not already exist at the target path.
// Returns an error if the encoded project directory exists.
func CheckConflict(encodedProjectDir string) error {
	_, err := os.Stat(encodedProjectDir)
	if err == nil {
		return fmt.Errorf("project directory %q already exists", encodedProjectDir)
	}
	return nil
}
