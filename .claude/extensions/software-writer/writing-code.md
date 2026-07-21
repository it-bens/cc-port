## Named-value assignments

- `project.stacks` = `go` (single stack; `go.mod`, Go 1.26)
- `code.primitives` =
  | Call shape | Reach for | In-repo helper | Invariant carried |
  |---|---|---|---|
  | Substring rewrite of a path-shaped string | `strings.ReplaceAll`, `bytes.ReplaceAll` | `rewrite.ReplacePathInBytes` (JSON-escaped input: `rewrite.ReplacePathInBytesWithJSONEscape`) | Plain replace corrupts prefix-sharing paths (`/a/foo` inside `/a/foo-extras`); boundary-aware match required. |
  | Count or probe path references in bytes | `strings.Count`, `bytes.Contains` | `rewrite.CountPathInBytes` / `rewrite.CountPathInBytesWithJSONEscape`; presence only: `rewrite.ContainsBoundedPath` | Plain counting matches prefix-sharing paths, so dry-run counts diverge from apply. |
  | Edit a key in `~/.claude.json` or any user-owned JSON file | `json.Marshal`/`json.Unmarshal` round-trip | `gjson` reads, `sjson` writes | Round-trip destroys key order, indentation, trailing newlines; the user-owned file's formatting is data. |
  | Rewrite a project path in a user-owned TOML file | `toml` round-trip, `strings.ReplaceAll` | `rewrite.TOMLPathRewrite` | Round-trip destroys comments and order; helper rewrites bytes boundary-aware, then parse-validates. |
  | Line-scan an untrusted JSONL stream or rules file | `bufio.NewScanner(r)` default buffer | Same scanner with explicit `.Buffer` cap; `claude.MaxHistoryLine` (16 MiB) for `history.jsonl` | Default 64 KiB buffer truncates silently; explicit cap rejects with `ErrTooLong`. |
  | Read an archive entry into the local fs | `os.OpenFile(filepath.Join(base, entry))` + hand-rolled containment | `archive.OpenReader` + `archive.StageSibling`, caps via `archive.Caps` | Carries `os.Root` containment, per-entry and aggregate decompression caps, `<tool>/` namespace check. |
  | Rewrite a path stored in a SQLite column | Hand-written `UPDATE` with `replace()`/`LIKE`; byte-editing the `.sqlite` file | `sqlrewrite.Open`, then `DB.Begin` and `DB.RewriteTextColumn` / `DB.UpdateColumnsByKey` inside the `*Tx`; count with `sqlrewrite.CountTextColumnRO` | `LIKE` wildcards/case-folds and SQL `replace()` corrupts prefix-sharing paths; wrapper carries byte-exact `substr` predicate, WAL checkpoint discipline, column validation. |
  | Compute the on-disk encoded directory for a project path | `claude.EncodePath(path)` directly | `tool.ResolveProjectPath(path)` first, then `claude.EncodePath` | Claude Code resolves symlinks before encoding (`/tmp/foo` → `-private-tmp-foo` on macOS); resolution walks the longest existing prefix. |
  | Resolve a destination through symlinks before file operations | inline `filepath.EvalSymlinks` on the full path | `fsutil.ResolveExistingAncestor` | Full path may not exist yet; helper evaluates the closest existing ancestor and re-attaches the tail. |
- `code.di_pattern` = Parameter or constructor-field injection; the composition root is `cmd/cc-port` (tool registry in `cmd/cc-port/tools.go`). Commands write through cobra's `cmd.OutOrStdout()`, never inline `os.Stdout`. A package-level fn-var seam (`var removeAll = os.RemoveAll`) is acceptable only for genuinely process-wide unconfigurable values, swapped under `t.Cleanup` in tests.
- `code.comment_enforcement` = revive's `exported` rule (enabled via `.golangci.yml`) requires doc comments on exported symbols; never delete them.

## Post-Step-6

A comment referencing material in a different module uses `see internal/<module>/README.md §<heading>` — file plus heading, never line numbers. Backlinks to the module's own adjacent README stay banned.
