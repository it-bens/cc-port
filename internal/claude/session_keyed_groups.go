package claude

import (
	"iter"
	"path/filepath"
)

// SessionKeyedGroup describes one of the five ~/.claude/ data groups keyed by
// session UUID. Consumers rely on the Name as the stable machine key and
// display label, on Files to enumerate a group's absolute paths from a
// ProjectLocations, and on SidecarFilter to exclude runtime-only basenames:
// when non-nil, SidecarFilter(basename) returning true means "skip this file".
type SessionKeyedGroup struct {
	Name          string
	Files         func(*ProjectLocations) []string
	SidecarFilter func(name string) bool
}

// SessionKeyedGroups is the canonical, ordered registry of session-keyed data
// groups.
var SessionKeyedGroups = []SessionKeyedGroup{
	{
		Name:  "todos",
		Files: func(l *ProjectLocations) []string { return l.TodoFiles },
	},
	{
		Name:  "usage-data/session-meta",
		Files: func(l *ProjectLocations) []string { return l.UsageDataSessionMeta },
	},
	{
		Name:  "usage-data/facets",
		Files: func(l *ProjectLocations) []string { return l.UsageDataFacets },
	},
	{
		Name:  "plugins-data",
		Files: func(l *ProjectLocations) []string { return l.PluginsDataFiles },
	},
	{
		Name:          "tasks",
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
