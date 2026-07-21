package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/tool/claude"
)

func TestFileHistoryLiteralsMatchClaudeAdapter(t *testing.T) {
	adapter := claude.New()
	assert.Equal(t, fileHistoryTool, adapter.Name())

	var found bool
	for _, category := range adapter.Categories() {
		if category.Name == "file-history" {
			found = true
			assert.Equal(t, fileHistoryEntryPrefix, category.Name+"/")
		}
	}
	require.True(t, found, "Claude adapter has no file-history category")
}
