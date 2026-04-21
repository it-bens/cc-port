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

Full flag reference: `cc-port <subcommand> --help`. `cc-port --version` prints the build version.

- `cc-port move <old-path> <new-path> [--apply] [--refs-only] [--rewrite-transcripts]`: rewrite every reference to `<old-path>` under `~/.claude/` to `<new-path>`. Default is dry-run. `--apply` copies, verifies, then deletes the old encoded directory. `--refs-only` updates references only and leaves the project directory in place on disk. `--rewrite-transcripts` also rewrites paths inside session transcripts.

  ```
  cc-port move /Users/me/old-project /Users/me/new-project --apply
  ```

- `cc-port export <project-path> --output <archive.zip>`: produce a portable archive of one project. Use `--all` or individual category flags (`--sessions`, `--memory`, `--history`, `--file-history`, `--config`, `--todos`, `--usage-data`, `--plugins-data`, `--tasks`). Omit all flags for an interactive picker.

  ```
  cc-port export /Users/me/project --output /tmp/project.zip --all
  ```

- `cc-port export manifest <project-path> [-o|--output <manifest.xml>]`: emit only the manifest for review or editing. Feed it back via `--from-manifest` on a subsequent `export` or `import`. Refuses to overwrite an existing output path.

  ```
  cc-port export manifest /Users/me/project --output /tmp/project.xml
  ```

- `cc-port import <archive.zip> <target-path>`: apply an archive to `<target-path>`. Placeholder resolutions come from `--resolution KEY=VALUE` flags or from a manifest via `--from-manifest`. Mixing `--from-manifest` with `--resolution` is rejected. Pick one source.

  ```
  cc-port import /tmp/project.zip /Users/teammate/project
  ```

- `cc-port import manifest <archive.zip> [-o|--output <manifest.xml>]`: read the metadata from an archive and write a manifest XML with empty resolve attributes for hand-editing. Feed it back via `--from-manifest` on a subsequent `import`. Refuses to overwrite an existing output path.

  ```
  cc-port import manifest /tmp/project.zip --output /tmp/project.xml
  ```

## Development

Contributing or modifying cc-port? See [`DEVELOPMENT.md`](DEVELOPMENT.md) for architecture, tests, lint, and commit conventions.

## License

See [`LICENSE`](LICENSE).
