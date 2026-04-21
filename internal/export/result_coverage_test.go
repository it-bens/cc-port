package export

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/manifest"
)

// TestResult_CoversEveryManifestCategory asserts categoryEntriesByName has
// a case for every entry in manifest.AllCategories. Adding a new category
// to the enum without wiring its slice into Result must fail this test.
//
// This is a `package export` internal test by necessity:
// categoryEntriesByName is unexported and the mapping is the invariant
// under test (UNIT-001 carve-out — cannot be observed externally).
func TestResult_CoversEveryManifestCategory(t *testing.T) {
	result := &Result{}
	for _, spec := range manifest.AllCategories {
		entries, err := categoryEntriesByName(result, spec.Name)
		require.NoError(t, err, "category %q must map to a Result slice", spec.Name)
		assert.NotNil(t, entries, "category %q returned nil slice pointer", spec.Name)
	}
}
