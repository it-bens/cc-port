package claude

import "github.com/it-bens/cc-port/internal/tool"

// Category wire names. Declared once so every reference (registries, move
// surfaces, stats surfaces, transcript/memory subdir filtering) shares one
// source of truth instead of repeating the literal.
const (
	categorySessions     = "sessions"
	categoryMemory       = "memory"
	categoryHistory      = "history"
	categoryFileHistory  = "file-history"
	categoryConfig       = "config"
	categoryConfigGrants = "config-grants"
	categoryTodos        = "todos"
	categoryUsageData    = "usage-data"
	categoryPluginsData  = "plugins-data"
	categoryTasks        = "tasks"
)

// categories is the source of truth for Claude Code's export categories:
// wire name, interactive-picker description, and default selection. Slice
// order is the canonical display order used by every consumer (CLI help,
// dry-run summaries, metadata.xml entries).
var categories = []tool.Category{
	{Name: categorySessions, Description: "Sessions (transcripts & subagent data)", DefaultSelected: true},
	{Name: categoryMemory, Description: "Memory (project-scoped auto-memory)", DefaultSelected: true},
	{Name: categoryHistory, Description: "History (command history entries)", DefaultSelected: true},
	{Name: categoryFileHistory, Description: "File history (file version snapshots)"},
	{Name: categoryConfig, Description: "Config (project config from ~/.claude.json)", DefaultSelected: true},
	{Name: categoryConfigGrants, Description: "Config grants (allowedTools permission grants)", ExcludedFromAll: true},
	{Name: categoryTodos, Description: "Todos (in-progress TodoWrite task lists)"},
	{Name: categoryUsageData, Description: "Usage data (session metadata + token facets)"},
	{Name: categoryPluginsData, Description: "Plugin data (per-session plugin state)"},
	{Name: categoryTasks, Description: "Tasks (numbered agent-task lists)"},
}
