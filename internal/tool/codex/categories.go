package codex

import "github.com/it-bens/cc-port/internal/tool"

// Category wire names. Declared once so registries, move surfaces, and stats
// surfaces share one source of truth instead of repeating the literal.
const (
	categorySessions = "sessions"
	categoryHistory  = "history"
)

// categories is the source of truth for Codex's export categories. There is
// no "config" category: trust is a per-machine decision and config.toml is
// never ported (spec §6.1).
var categories = []tool.Category{
	{Name: categorySessions, Description: "Sessions (rollout transcripts)", DefaultSelected: true},
	{Name: categoryHistory, Description: "History (command history entries)", DefaultSelected: true},
}
