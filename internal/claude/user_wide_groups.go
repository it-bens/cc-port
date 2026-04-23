package claude

// UserWideRewriteTarget describes one user-wide ~/.claude/ file whose bytes
// may contain references to a project path. Move iterates this registry and
// applies boundary-aware byte replacement through rewrite.ReplacePathInBytes
// on each target. Export and import intentionally do not consume it: these
// files are machine-local.
type UserWideRewriteTarget struct {
	Name string
	Path func(*Home) string
}

// UserWideRewriteTargets is the canonical, ordered registry of user-wide
// rewrite targets. Slice order is the display and iteration order used by
// every downstream consumer (plan-count keys, Apply's rewrite loop).
var UserWideRewriteTargets = []UserWideRewriteTarget{
	{Name: "settings", Path: (*Home).SettingsFile},
	{Name: "plugins/installed_plugins", Path: (*Home).PluginsInstalledFile},
	{Name: "plugins/known_marketplaces", Path: (*Home).KnownMarketplacesFile},
}
