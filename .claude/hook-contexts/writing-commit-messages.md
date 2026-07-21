## Pre-Step-8

`.claude/**` content (skills, extensions, hook contexts, rules, settings) is code, not documentation: classify such changes as `feat`, `fix`, or `refactor`, never `docs`.

## Pre-Step-9

Infer the scope from cc-port's vocabulary instead of the top-level-directory default:

- `internal/<module>/**` → the module basename (`importer`, `move`, `export`, `sync`, `stats`, `lock`, `manifest`, `rewrite`, `sqlrewrite`, `archive`, `scan`, `fsutil`, `testutil`, `ui`, ...); adapters map to their tool: `internal/tool/claude/**` → `claude`, `internal/tool/codex/**` → `codex`.
- `cmd/cc-port/**` → `cmd`.
- Dependency bumps (`go.mod`/`go.sum`, GitHub Action versions) → `deps`.
- Other `.github/workflows/**` changes → `ci`; `.goreleaser.yml` and release signing → `release`.
- `.claude/skills/**` → `skills`; `.claude/hook-contexts/**` and hook wiring in `.claude/settings.json` → `hooks`; remaining `.claude/**` plus root `CLAUDE.md` / `AGENTS.md` / `AGENTS.override.md` → `claude`.
- The demo GIF/MP4 assets under `docs/images/` and their generation pipeline → `videos`.
