package export

import "github.com/it-bens/cc-port/internal/manifest"

// ApplyPlaceholders is a test-only export of applyPlaceholders, used by
// regression tests that compare streaming output against a known-good
// reference transform. Kept in an _test.go file so no production caller
// can depend on it.
func ApplyPlaceholders(data []byte, placeholders []manifest.Placeholder) []byte {
	return applyPlaceholders(data, placeholders)
}
