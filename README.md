# cc-port

`cc-port` rewrites Claude Code project state after a rename, an export, or an import. Moving a project directory on disk or handing it to a teammate invalidates the absolute paths baked into `~/.claude/projects/<encoded>/`, `~/.claude/history.jsonl`, and `~/.claude.json`. cc-port rewrites the references safely: boundary-aware substring replacement, atomic writes with rollback, and a lock-plus-live-session check. No operation collides with a running Claude Code process.

## Install

Homebrew (this repo's tap):

```
brew install it-bens/tap/cc-port
```

Or with `go install`:

```
go install github.com/it-bens/cc-port/cmd/cc-port@latest
```

Prebuilt releases (macOS / Linux tarballs, checksums) are published under [GitHub Releases](https://github.com/it-bens/cc-port/releases).

## Commands

Full flag reference: `cc-port <subcommand> --help`.

- `cc-port move <old-path> <new-path> [--apply]`: rewrite every reference to `<old-path>` under `~/.claude/` to `<new-path>`. Default is dry-run. `--apply` copies, verifies, then deletes the old encoded directory.

  ```
  cc-port move /Users/me/old-project /Users/me/new-project --apply
  ```

- `cc-port export <project-path> --output <archive.zip>`: produce a portable archive of one project. Use `--all` or individual category flags (`--sessions`, `--memory`, `--history`, `--file-history`, `--config`, `--todos`, `--usage-data`, `--plugins-data`, `--tasks`). Omit all flags for an interactive picker.

  ```
  cc-port export /Users/me/project --output /tmp/project.zip --all
  ```

- `cc-port export manifest <project-path> [--output <manifest.xml>]`: emit only the manifest for review or editing. Feed it back via `--from-manifest` on a subsequent `export` or `import`.

  ```
  cc-port export manifest /Users/me/project --output /tmp/project.xml
  ```

- `cc-port import <archive.zip> <target-path>`: apply an archive to `<target-path>`. Placeholder resolutions come from `--resolution KEY=VALUE` flags or from a manifest via `--from-manifest`.

  ```
  cc-port import /tmp/project.zip /Users/teammate/project
  ```

## Development

Contributing or modifying cc-port? See [`DEVELOPMENT.md`](DEVELOPMENT.md) for architecture, tests, lint, and commit conventions.

## License

See [`LICENSE`](LICENSE).
