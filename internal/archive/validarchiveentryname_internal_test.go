package archive

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidArchiveEntryName_RejectsDotSegments(t *testing.T) {
	tests := map[string]bool{
		"memory/../secret": false,
		"a/./b":            false,
		"/abs":             false,
		"..":               false,
		"sessions/a.jsonl": true,
		"note.txt":         true,
	}

	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, validArchiveEntryName(name))
		})
	}
}
