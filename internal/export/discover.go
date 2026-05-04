package export

import (
	"regexp"
	"sort"
	"strings"
)

// PlaceholderSuggestion pairs a placeholder key with the original path it replaces.
type PlaceholderSuggestion struct {
	Key      string // e.g., "{{PROJECT_PATH}}"
	Original string // e.g., "/Users/test/Projects/myproject"
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

// AutoDetectPlaceholders maps each prefix that exactly matches projectPath
// or homePath to its placeholder name. Unknown prefixes are dropped: with
// the strict anchor filter upstream, only the two anchors ever survive.
func AutoDetectPlaceholders(prefixes []string, projectPath, homePath string) []PlaceholderSuggestion {
	suggestions := make([]PlaceholderSuggestion, 0, len(prefixes))
	for _, prefix := range prefixes {
		switch prefix {
		case projectPath:
			suggestions = append(suggestions, PlaceholderSuggestion{
				Key:      "{{PROJECT_PATH}}",
				Original: prefix,
			})
		case homePath:
			suggestions = append(suggestions, PlaceholderSuggestion{
				Key:      "{{HOME}}",
				Original: prefix,
			})
		}
	}
	return suggestions
}

// DiscoverPlaceholders returns placeholder suggestions for path references
// in content, anchored under projectPath and homePath. Both anchors must be
// cleaned absolute non-root paths; the CLI layer enforces this via
// resolveHomeAnchor and claude.ResolveProjectPath.
func DiscoverPlaceholders(content []byte, projectPath, homePath string) []PlaceholderSuggestion {
	candidates := DiscoverPaths(content)
	filtered := filterAnchored(candidates, projectPath, homePath)
	prefixes := referencedAnchors(filtered, projectPath, homePath)
	return AutoDetectPlaceholders(prefixes, projectPath, homePath)
}

// filterAnchored keeps only candidates that equal one of the anchors or
// sit under it (anchor + "/" boundary). Empty anchors are skipped so a
// missing input never matches everything.
func filterAnchored(candidates []string, projectPath, homePath string) []string {
	anchors := [2]string{projectPath, homePath}
	var result []string
	for _, candidate := range candidates {
		for _, anchor := range anchors {
			if anchor == "" {
				continue
			}
			if candidate == anchor || strings.HasPrefix(candidate, anchor+"/") {
				result = append(result, candidate)
				break
			}
		}
	}
	return result
}

// referencedAnchors returns the subset of {projectPath, homePath} that has
// at least one hit in candidates. Sorted longest-first so applyPlaceholders'
// longest-first substitution emits the longer match before the shorter one.
// Deduped: when projectPath == homePath, the single anchor is emitted once
// (project takes precedence in AutoDetectPlaceholders' switch).
func referencedAnchors(candidates []string, projectPath, homePath string) []string {
	hasProject, hasHome := false, false
	for _, candidate := range candidates {
		if !hasProject && projectPath != "" &&
			(candidate == projectPath || strings.HasPrefix(candidate, projectPath+"/")) {
			hasProject = true
		}
		if !hasHome && homePath != "" &&
			(candidate == homePath || strings.HasPrefix(candidate, homePath+"/")) {
			hasHome = true
		}
		if hasProject && hasHome {
			break
		}
	}
	seen := map[string]struct{}{}
	var result []string
	add := func(value string) {
		if value == "" {
			return
		}
		if _, dup := seen[value]; dup {
			return
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if hasProject {
		add(projectPath)
	}
	if hasHome {
		add(homePath)
	}
	sort.Slice(result, func(i, j int) bool {
		return len(result[i]) > len(result[j])
	})
	return result
}
