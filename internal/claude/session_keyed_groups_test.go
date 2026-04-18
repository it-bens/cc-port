package claude

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionKeyedGroups_CanonicalOrder(t *testing.T) {
	want := []string{
		"todos",
		"usage-data/session-meta",
		"usage-data/facets",
		"plugins-data",
		"tasks",
	}
	require.Len(t, SessionKeyedGroups, len(want))
	for i, group := range SessionKeyedGroups {
		assert.Equal(t, want[i], group.Name, "position %d", i)
	}
}

func TestIsTaskSidecar(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{".lock", true},
		{".highwatermark", true},
		{"0.json", false},
		{"anything.json", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTaskSidecar(tc.name))
		})
	}
}

func TestAllFlatFiles_FiltersTaskSidecars(t *testing.T) {
	base := "/home/u/.claude"
	locations := &ProjectLocations{
		TodoFiles:            []string{filepath.Join(base, "todos", "a-agent-a.json")},
		UsageDataSessionMeta: []string{filepath.Join(base, "usage-data", "session-meta", "a.json")},
		UsageDataFacets:      []string{filepath.Join(base, "usage-data", "facets", "a.json")},
		PluginsDataFiles:     []string{filepath.Join(base, "plugins", "data", "ns", "a", "blob.json")},
		TaskFiles: []string{
			filepath.Join(base, "tasks", "a", "0.json"),
			filepath.Join(base, "tasks", "a", ".lock"),
			filepath.Join(base, "tasks", "a", ".highwatermark"),
		},
	}

	type pair struct {
		group string
		path  string
	}
	var got []pair
	for group, path := range locations.AllFlatFiles() {
		got = append(got, pair{group.Name, path})
	}

	want := []pair{
		{"todos", filepath.Join(base, "todos", "a-agent-a.json")},
		{"usage-data/session-meta", filepath.Join(base, "usage-data", "session-meta", "a.json")},
		{"usage-data/facets", filepath.Join(base, "usage-data", "facets", "a.json")},
		{"plugins-data", filepath.Join(base, "plugins", "data", "ns", "a", "blob.json")},
		{"tasks", filepath.Join(base, "tasks", "a", "0.json")},
	}
	assert.Equal(t, want, got)
}

func TestAllFlatFiles_EarlyTermination(t *testing.T) {
	locations := &ProjectLocations{
		TodoFiles:            []string{"/a/todos/x.json"},
		UsageDataSessionMeta: []string{"/a/usage-data/session-meta/x.json"},
		UsageDataFacets:      []string{"/a/usage-data/facets/x.json"},
		PluginsDataFiles:     []string{"/a/plugins/data/ns/x/blob.json"},
		TaskFiles:            []string{"/a/tasks/x/0.json"},
	}

	count := 0
	require.NotPanics(t, func() {
		for range locations.AllFlatFiles() {
			count++
			break
		}
	})
	assert.Equal(t, 1, count)
}
