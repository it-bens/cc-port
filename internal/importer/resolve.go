package importer

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const unresolvedMarker = "UNRESOLVED"

// ResolvePlaceholders replaces placeholder strings in content with their resolved values.
// Unresolved placeholders (not in the map) are left as-is.
func ResolvePlaceholders(content []byte, resolutions map[string]string) []byte {
	result := content
	for placeholder, value := range resolutions {
		result = bytes.ReplaceAll(result, []byte(placeholder), []byte(value))
	}
	return result
}

// ValidateResolutions checks that all resolutions are valid absolute paths.
// Empty values are only allowed for UNRESOLVED placeholders.
func ValidateResolutions(resolutions map[string]string) error {
	for placeholder, value := range resolutions {
		if strings.Contains(placeholder, unresolvedMarker) {
			continue
		}
		if value == "" {
			return fmt.Errorf("resolution for %q is empty but placeholder is not UNRESOLVED", placeholder)
		}
		if !filepath.IsAbs(value) {
			return fmt.Errorf("resolution for %q is not an absolute path: %q", placeholder, value)
		}
	}
	return nil
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
