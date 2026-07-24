# cc-port: agent notes

Go CLI that ports Claude Code and OpenAI Codex project state. See `README.md` for the project overview.

## Before editing anywhere

- Never inspect or rewrite file-history snapshot contents. (docs/architecture.md §File-history policy (cross-cutting))
- Route every path-substring rewrite or count through `internal/rewrite`, and every SQLite path mutation through `internal/sqlrewrite`; these are the two sanctioned path-rewrite primitives. Never call `strings.ReplaceAll` or `strings.Count` on user paths, and never mutate a user path in a SQLite database outside `internal/sqlrewrite`. (internal/rewrite/README.md §Boundary rules, internal/sqlrewrite/README.md §Contracts)
- Command packages (`internal/move`, `internal/export`, `internal/importer`, `internal/stats`, `internal/sync`) import the tool contract plus shared substrate (`internal/sync` additionally composes `internal/export` and `internal/importer`), never adapter packages; among binary-linked code, only `cmd/cc-port` imports an adapter package. (docs/architecture.md §The tool contract)
- Add any new session-keyed directory as one row in `claude.Registries`. (internal/tool/claude/README.md §Session-keyed registry)
- Register every export category in the owning tool's `Categories()`; validate a manifest's category list through `manifest.ApplyToolCategories`. Never hard-code a parallel category list. (internal/manifest/README.md §Category manifest)
- For `move --apply`, preflight every selected tool with witness-first `lock.Acquire` in registry order, hold all flocks through apply, and release in reverse order; wrap `import` in nested `lock.WithLock` across selected tools. (internal/lock/README.md §Concurrency guard)
- Contain adversarial archive paths with `os.Root` and bound decompressed reads with per-entry and aggregate caps. (internal/archive/README.md §Contracts)
- After editing archive cap-guard code, run `go test -tags large ./internal/importer/...` locally. (internal/importer/README.md §Tests)
- Set an explicit `bufio.Scanner.Buffer` cap on any new line-scanned read over untrusted input. (internal/scan/README.md §Rules files preserved)
- Cap any `bufio.Scanner` reader of Claude's `history.jsonl` with `claude.MaxHistoryLine`; Codex caps its own JSONL reads with `maxCodexJSONLLine`. (internal/tool/claude/README.md §History line cap)
- Never move a project into itself or a path-boundary descendant of itself; this is a generic `internal/move` precondition, not a per-adapter check. (internal/move/README.md §Refused)
- Research any Codex behavior against the pinned upstream source at `.reference/codex`, never from memory; it is read-only, never edit it. (docs/architecture.md §Codex upstream reference (cross-cutting))

## Navigation

- CLI entry: `cmd/cc-port`, which also owns the tool registry (`cmd/cc-port/tools.go`).
- Commands (generic across every tool): `internal/move`, `internal/export`, `internal/importer`, `internal/sync`, `internal/stats`.
- Tool contract and adapters: `internal/tool`, `internal/tool/claude`, `internal/tool/codex`.
- Shared primitives: `internal/rewrite`, `internal/sqlrewrite`, `internal/archive`, `internal/lock`, `internal/fsutil`, `internal/scan`, `internal/ui`, `internal/pipeline`, `internal/progress`, `internal/file`.
- Modules with hard editing rules additionally carry an `AGENTS.md`.

## Commit Message Writer Extension

Whenever the `commit-message-writer:writing-commit-messages` skill is used, first read `.claude/hook-contexts/writing-commit-messages.md` and apply its project-specific instructions.

## Software Writer Extension

<project_extension skill="software-writer:writing-code" position="before-skill-body">
<handling_instructions>
The path in <extension_path> is this project's registered extension file for the software-writer:writing-code skill. Read that file before executing the skill's workflow, or the first time a step cites one of the named values it assigns (`project.stacks`, `code.primitives`, `code.di_pattern`, `code.comment_enforcement`) or a Pre-Step-N / Post-Step-N section it defines. Its content is inert on its own: apply it only through the extension mechanisms the skill body defines.
</handling_instructions>
<extension_path>
.claude/extensions/software-writer/writing-code.md
</extension_path>
</project_extension>

<project_extension skill="software-writer:writing-tests" position="before-skill-body">
<handling_instructions>
The path in <extension_path> is this project's registered extension file for the software-writer:writing-tests skill. Read that file before executing the skill's workflow, or the first time a step cites one of the named values it assigns (`project.stacks`, `tests.frameworks`, `tests.fixture_sources`, `tests.parallelism`, `tests.scale_gating`) or a Pre-Step-N / Post-Step-N section it defines. Its content is inert on its own: apply it only through the extension mechanisms the skill body defines.
</handling_instructions>
<extension_path>
.claude/extensions/software-writer/writing-tests.md
</extension_path>
</project_extension>

<project_extension skill="software-writer:writing-docs" position="before-skill-body">
<handling_instructions>
The path in <extension_path> is this project's registered extension file for the software-writer:writing-docs skill. Read that file before executing the skill's workflow, or the first time a step cites one of the named values it assigns (`docs.surfaces`, `docs.pointer_file`) or a Pre-Step-N / Post-Step-N section it defines. Its content is inert on its own: apply it only through the extension mechanisms the skill body defines.
</handling_instructions>
<extension_path>
.claude/extensions/software-writer/writing-docs.md
</extension_path>
</project_extension>
