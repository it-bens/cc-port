package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/it-bens/cc-port/internal/export"
)

func TestCategorizFromMetadata_HardFailsOnUnknown(t *testing.T) {
	metadata := &export.Metadata{
		Export: export.Info{
			Categories: []export.Category{
				{Name: "sessions", Included: true},
				{Name: "bogus-category", Included: true},
			},
		},
	}
	_, err := categorizFromMetadata(metadata)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus-category")
}

func TestCategorizFromMetadata_AcceptsAllNineKnown(t *testing.T) {
	metadata := &export.Metadata{
		Export: export.Info{
			Categories: []export.Category{
				{Name: "sessions", Included: true},
				{Name: "memory", Included: true},
				{Name: "history", Included: true},
				{Name: "file-history", Included: true},
				{Name: "config", Included: true},
				{Name: "todos", Included: true},
				{Name: "usage-data", Included: true},
				{Name: "plugins-data", Included: true},
				{Name: "tasks", Included: true},
			},
		},
	}
	cat, err := categorizFromMetadata(metadata)
	require.NoError(t, err)
	assert.True(t, cat.Sessions)
	assert.True(t, cat.Todos)
	assert.True(t, cat.UsageData)
	assert.True(t, cat.PluginsData)
	assert.True(t, cat.Tasks)
}
