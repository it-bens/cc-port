package claude

import (
	"iter"
	"path/filepath"
)

// RegistryEntry describes a Claude Code storage surface.
type RegistryEntry struct {
	Name          string
	Category      string
	Files         func(*ProjectLocations) []string
	SidecarFilter func(name string) bool
	ZipPrefix     string
	HomeBaseDir   func(*Home) string
	RewritePath   func(*Home) string
}

// Registries is the ordered source of truth for Claude Code storage surfaces.
var Registries = []RegistryEntry{
	{
		Name:        "todos",
		Category:    "todos",
		Files:       func(locations *ProjectLocations) []string { return locations.TodoFiles },
		ZipPrefix:   "todos/",
		HomeBaseDir: (*Home).TodosDir,
	},
	{
		Name:      "usage-data/session-meta",
		Category:  "usage-data",
		Files:     func(locations *ProjectLocations) []string { return locations.UsageDataSessionMeta },
		ZipPrefix: "usage-data/session-meta/",
		HomeBaseDir: func(home *Home) string {
			return filepath.Join(home.UsageDataDir(), "session-meta")
		},
	},
	{
		Name:      "usage-data/facets",
		Category:  "usage-data",
		Files:     func(locations *ProjectLocations) []string { return locations.UsageDataFacets },
		ZipPrefix: "usage-data/facets/",
		HomeBaseDir: func(home *Home) string {
			return filepath.Join(home.UsageDataDir(), "facets")
		},
	},
	{
		Name:        "plugins-data",
		Category:    "plugins-data",
		Files:       func(locations *ProjectLocations) []string { return locations.PluginsDataFiles },
		ZipPrefix:   "plugins-data/",
		HomeBaseDir: (*Home).PluginsDataDir,
	},
	{
		Name:          "tasks",
		Category:      "tasks",
		Files:         func(locations *ProjectLocations) []string { return locations.TaskFiles },
		SidecarFilter: isTaskSidecar,
		ZipPrefix:     "tasks/",
		HomeBaseDir:   (*Home).TasksDir,
	},
	{Name: "settings", RewritePath: (*Home).SettingsFile},
	{Name: "plugins/installed_plugins", RewritePath: (*Home).PluginsInstalledFile},
	{Name: "plugins/known_marketplaces", RewritePath: (*Home).KnownMarketplacesFile},
}

func isTaskSidecar(name string) bool {
	return name == ".lock" || name == ".highwatermark"
}

// SessionKeyedGroups yields session-keyed entries in registry order.
func SessionKeyedGroups() iter.Seq[RegistryEntry] {
	return func(yield func(RegistryEntry) bool) {
		for _, entry := range Registries {
			if entry.Files != nil && !yield(entry) {
				return
			}
		}
	}
}

// UserWideRewriteTargets yields user-wide rewrite entries in registry order.
func UserWideRewriteTargets() iter.Seq[RegistryEntry] {
	return func(yield func(RegistryEntry) bool) {
		for _, entry := range Registries {
			if entry.RewritePath != nil && !yield(entry) {
				return
			}
		}
	}
}

// AllFlatFiles yields registry entries and absolute paths in registry order.
func (locations *ProjectLocations) AllFlatFiles() iter.Seq2[RegistryEntry, string] {
	return func(yield func(RegistryEntry, string) bool) {
		for entry := range SessionKeyedGroups() {
			for _, path := range entry.Files(locations) {
				if entry.SidecarFilter != nil && entry.SidecarFilter(filepath.Base(path)) {
					continue
				}
				if !yield(entry, path) {
					return
				}
			}
		}
	}
}
