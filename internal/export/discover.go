package export

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// PlaceholderSuggestion pairs a placeholder key with the original path it replaces.
type PlaceholderSuggestion struct {
	Key      string // e.g., "{{PROJECT_PATH}}"
	Original string // e.g., "/Users/test/Projects/myproject"
	Auto     bool   // true if auto-detected
}

// pathPattern rejects trailing dots and slashes so cleaned paths always end at
// a real segment character.
var pathPattern = regexp.MustCompile(`/[a-zA-Z0-9_\-./]+[a-zA-Z0-9_\-]`)

// DiscoverPaths extracts unique absolute paths from content.
func DiscoverPaths(content []byte) []string {
	matches := pathPattern.FindAll(content, -1)

	seen := make(map[string]struct{})
	var paths []string

	for _, match := range matches {
		cleaned := strings.TrimRight(string(match), "./")
		if cleaned == "" || cleaned == "/" {
			continue
		}
		if _, alreadySeen := seen[cleaned]; alreadySeen {
			continue
		}
		seen[cleaned] = struct{}{}
		paths = append(paths, cleaned)
	}

	return paths
}

// GroupPathPrefixes finds meaningful common path prefixes from a list of paths,
// sorted longest first. A path is excluded if it is a sub-path of another
// prefix in the result.
func GroupPathPrefixes(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	parentCoverage := countParentCoverage(paths)
	winningPrefixes := findWinningPrefixes(parentCoverage)

	// Start with the winning prefix set. Sort shortest first so that
	// more-general prefixes are accepted before their sub-paths, allowing
	// sub-paths to be filtered out as redundant.
	sort.Slice(winningPrefixes, func(i, j int) bool {
		return len(winningPrefixes[i]) < len(winningPrefixes[j])
	})

	seenPaths := make(map[string]struct{})
	var result []string

	for _, prefix := range winningPrefixes {
		if _, alreadyAdded := seenPaths[prefix]; alreadyAdded {
			continue
		}
		coveredByExistingPrefix := false
		for _, kept := range result {
			if strings.HasPrefix(prefix, kept+"/") {
				coveredByExistingPrefix = true
				break
			}
		}
		if !coveredByExistingPrefix {
			seenPaths[prefix] = struct{}{}
			result = append(result, prefix)
		}
	}

	for _, path := range paths {
		if _, alreadyAdded := seenPaths[path]; alreadyAdded {
			continue
		}
		coveredByPrefix := false
		for _, prefix := range winningPrefixes {
			if strings.HasPrefix(path, prefix+"/") || path == prefix {
				coveredByPrefix = true
				break
			}
		}
		if !coveredByPrefix {
			seenPaths[path] = struct{}{}
			result = append(result, path)
		}
	}

	// Sort result longest first for the return value.
	sort.Slice(result, func(i, j int) bool {
		return len(result[i]) > len(result[j])
	})

	return result
}

// countParentCoverage counts how many input paths each ancestor directory covers.
func countParentCoverage(paths []string) map[string]int {
	parentCoverage := make(map[string]int)
	for _, path := range paths {
		parent := filepath.Dir(path)
		for parent != "/" && parent != "." && parent != "" {
			parentCoverage[parent]++
			parent = filepath.Dir(parent)
		}
	}
	return parentCoverage
}

// findWinningPrefixes returns the most specific qualifying parents: those with
// coverage >= 2 and no equally covering child.
func findWinningPrefixes(parentCoverage map[string]int) []string {
	var qualifyingParents []string
	for parent, count := range parentCoverage {
		if count >= 2 {
			qualifyingParents = append(qualifyingParents, parent)
		}
	}

	// Sort longest first so we can check children efficiently.
	sort.Slice(qualifyingParents, func(i, j int) bool {
		return len(qualifyingParents[i]) > len(qualifyingParents[j])
	})

	// A qualifying parent is "winning" if no more-specific qualifying parent
	// covers ALL of the same paths (i.e., has the same coverage count).
	var winningPrefixes []string
	for _, parent := range qualifyingParents {
		parentCount := parentCoverage[parent]
		hasEquallyCoveringChild := false
		for _, other := range qualifyingParents {
			if len(other) > len(parent) &&
				strings.HasPrefix(other, parent+"/") &&
				parentCoverage[other] == parentCount {
				hasEquallyCoveringChild = true
				break
			}
		}
		if !hasEquallyCoveringChild {
			winningPrefixes = append(winningPrefixes, parent)
		}
	}
	return winningPrefixes
}

// AutoDetectPlaceholders assigns placeholder names to a list of path prefixes.
// projectPath maps to {{PROJECT_PATH}}, homePath maps to {{HOME}}, and all
// remaining prefixes receive {{UNRESOLVED_N}} names starting from 1.
func AutoDetectPlaceholders(prefixes []string, projectPath, homePath string) []PlaceholderSuggestion {
	suggestions := make([]PlaceholderSuggestion, 0, len(prefixes))
	unresolvedCounter := 1

	for _, prefix := range prefixes {
		switch prefix {
		case projectPath:
			suggestions = append(suggestions, PlaceholderSuggestion{
				Key:      "{{PROJECT_PATH}}",
				Original: prefix,
				Auto:     true,
			})
		case homePath:
			suggestions = append(suggestions, PlaceholderSuggestion{
				Key:      "{{HOME}}",
				Original: prefix,
				Auto:     true,
			})
		default:
			suggestions = append(suggestions, PlaceholderSuggestion{
				Key:      fmt.Sprintf("{{UNRESOLVED_%d}}", unresolvedCounter),
				Original: prefix,
				Auto:     false,
			})
			unresolvedCounter++
		}
	}

	return suggestions
}
