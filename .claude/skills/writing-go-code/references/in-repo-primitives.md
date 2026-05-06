# In-Repo Primitives (reference)

Loaded from `writing-go-code` SKILL.md when the *Confirm the API call* step
identifies a domain mutation: a call that touches data the codebase has already
taken responsibility for. Stdlib calls that look right in isolation often
violate an invariant the project enforces only through a wrapper.

## Lookup table

Search the repo for the helper before reaching for the stdlib call directly.

| Call shape | Reach for | In-repo helper | Reason the wrapper exists |
|---|---|---|---|
| Substring rewrite of a path-shaped string | `strings.ReplaceAll`, `bytes.ReplaceAll` | `rewrite.ReplacePathInBytes` (or `rewrite.ReplacePathInBytesWithJSONEscape` when the input may contain JSON-escaped slashes) | Plain `ReplaceAll` corrupts paths sharing a prefix: `/a/foo` inside `/a/foo-extras` produces `/a/renamed-extras`. Boundary-aware match is required. |
| Edit a key in `~/.claude.json` or any user-owned JSON file | `json.Marshal` / `json.Unmarshal` round-trip | `gjson` reads, `sjson` writes | Round-trip destroys key order, indentation, and trailing newlines. The user-owned file's formatting is data; preserve it. |
| Line-scan an untrusted JSONL stream or rules file | `bufio.NewScanner(r)` with default buffer | Same scanner with `.Buffer(make([]byte, 0, claude.MaxHistoryLine), claude.MaxHistoryLine)` | Default 64 KiB buffer truncates long lines silently and rejects adversarial input as `ErrTooLong` only when explicitly capped. The 16 MiB cap is the project-wide invariant. |
| Read an archive entry into the local fs | `os.OpenFile(filepath.Join(base, entry))` | `os.Root` opened on the staging base, with `io.LimitReader` on the entry stream | Without `os.Root`, an archive `..`-traversal escapes the staging directory. Without `LimitReader`, a zip bomb exhausts memory before staging fails. |
| Compute the on-disk encoded directory for a project path | `EncodePath(path)` directly | `claude.ResolveProjectPath(path)` first, then `EncodePath` | Claude Code resolves symlinks before encoding. `/tmp/foo` on macOS becomes `-private-tmp-foo`, not `-tmp-foo`. The resolution must walk to the longest existing prefix and `EvalSymlinks` it, since the new path may not exist yet. |
| Resolve a destination through symlinks before file operations | inline `filepath.EvalSymlinks` on the full path | `fsutil.ResolveExistingAncestor` | The full path may not exist yet (move destinations, staging temps). The helper walks up to the closest existing ancestor, evaluates symlinks there, and re-attaches the missing tail unchanged. |

## Decision Test

Before writing the stdlib call, ask:

> Does this call touch user paths, user-owned config, untrusted byte streams,
> or archive entries — and does the project already wrap the stdlib primitive
> for that case?

- yes, helper exists → use the helper
- yes, no helper exists → flag and propose adding one rather than reaching past the missing wrapper
- no → stdlib is fine

## Why this lookup precedes `go doc`

`go doc` confirms the stdlib call works the way you remember. The in-repo
lookup confirms the stdlib call is the right call to make at all. Both run
before writing — `go doc` for *correctness of the call*, in-repo for
*fitness of the call*.

## What the lookup is not

The table is not exhaustive. It captures the categories where missed routing
through the helper has caused a recurring class of bug. New categories appear
when a new wrapper lands; extend the table when it does. Treat the table as
evidence-based, not definitional: if a stdlib call sits clearly outside these
categories, the table does not apply.
