package claude

import (
	"iter"
	"path/filepath"
)

// SessionKeyedGroup describes one of the ~/.claude/ data groups keyed by
// session UUID. Consumers rely on the Name as the stable machine key and
// display label, on Files to enumerate a group's absolute paths from a
// ProjectLocations, on SidecarFilter to exclude runtime-only basenames
// (when non-nil, SidecarFilter(basename) returning true means "skip this
// file"), and on Category to gate the group against a manifest.AllCategories
// entry.
type SessionKeyedGroup struct {
	Name          string
	Category      string
	Files         func(*ProjectLocations) []string
	SidecarFilter func(name string) bool
}

// SessionKeyedGroups is the canonical, ordered registry of session-keyed data
// groups.
var SessionKeyedGroups = []SessionKeyedGroup{
	{
		Name:     "todos",
		Category: "todos",
		Files:    func(l *ProjectLocations) []string { return l.TodoFiles },
	},
	{
		Name:     "usage-data/session-meta",
		Category: "usage-data",
		Files:    func(l *ProjectLocations) []string { return l.UsageDataSessionMeta },
	},
	{
		Name:     "usage-data/facets",
		Category: "usage-data",
		Files:    func(l *ProjectLocations) []string { return l.UsageDataFacets },
	},
	{
		Name:     "plugins-data",
		Category: "plugins-data",
		Files:    func(l *ProjectLocations) []string { return l.PluginsDataFiles },
	},
	{
		Name:          "tasks",
		Category:      "tasks",
		Files:         func(l *ProjectLocations) []string { return l.TaskFiles },
		SidecarFilter: isTaskSidecar,
	},
}

func isTaskSidecar(name string) bool {
	return name == ".lock" || name == ".highwatermark"
}

// AllFlatFiles yields (group, absolute path) pairs in canonical registry order,
// applying each group's SidecarFilter. The iterator performs no I/O and
// supports early termination via break.
func (l *ProjectLocations) AllFlatFiles() iter.Seq2[SessionKeyedGroup, string] {
	return func(yield func(SessionKeyedGroup, string) bool) {
		for _, group := range SessionKeyedGroups {
			for _, path := range group.Files(l) {
				if group.SidecarFilter != nil && group.SidecarFilter(filepath.Base(path)) {
					continue
				}
				if !yield(group, path) {
					return
				}
			}
		}
	}
}
