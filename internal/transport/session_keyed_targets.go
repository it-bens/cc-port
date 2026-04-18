// Package transport holds zip-layout descriptors shared by export and import.
// It exists as a third package so neither sibling (internal/export,
// internal/importer) has to import the other to agree on the wire format.
package transport

import (
	"path/filepath"

	"github.com/it-bens/cc-port/internal/claude"
)

// SessionKeyedTarget describes one session-keyed group's transport footprint:
// which zip prefix encodes its entries and which home directory receives them
// on import. The slice position of a target MUST match the slice position of
// the group with the same Name in claude.SessionKeyedGroups — downstream code
// pairs registries by index without a runtime check.
type SessionKeyedTarget struct {
	Group       string
	ZipPrefix   string
	HomeBaseDir func(*claude.Home) string
}

// SessionKeyedTargets is the zip-layout registry for session-keyed groups.
// Order is index-aligned with claude.SessionKeyedGroups; ZipPrefix values are
// unique and slash-terminated so the importer can dispatch on prefix match.
var SessionKeyedTargets = []SessionKeyedTarget{
	{
		Group:       "todos",
		ZipPrefix:   "todos/",
		HomeBaseDir: func(home *claude.Home) string { return home.TodosDir() },
	},
	{
		Group:     "usage-data/session-meta",
		ZipPrefix: "usage-data/session-meta/",
		HomeBaseDir: func(home *claude.Home) string {
			return filepath.Join(home.UsageDataDir(), "session-meta")
		},
	},
	{
		Group:     "usage-data/facets",
		ZipPrefix: "usage-data/facets/",
		HomeBaseDir: func(home *claude.Home) string {
			return filepath.Join(home.UsageDataDir(), "facets")
		},
	},
	{
		Group:       "plugins-data",
		ZipPrefix:   "plugins-data/",
		HomeBaseDir: func(home *claude.Home) string { return home.PluginsDataDir() },
	},
	{
		Group:       "tasks",
		ZipPrefix:   "tasks/",
		HomeBaseDir: func(home *claude.Home) string { return home.TasksDir() },
	},
}
