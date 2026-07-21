# cc-port design rules

Task-keyed rules. Each entry names a task, the way that holds in cc-port, and the failure modes the right way avoids.

## Substitute one project path for another in user-owned data

Enumerate every shape the path can appear in before writing the rewriter — top-level fields, nested JSON keys (`mcpServers.*.args`, `mcpServers.*.env`, `mcpContextUris`, `exampleFiles`), JSONL line bodies, free-text fields, file basenames, JSON-escaped slashes (`\/`), the encoded storage-dir form (`-Users-...`, never matched by the slashed-path rewriter; carry it as a distinct placeholder or rewrite pass keyed on the absolute `~/.claude/projects/<encoded>` path), prefix-sharing names where `/a/foo` and `/a/foo-extras` both appear. Pick `rewrite.ReplacePathInBytes` per surface, or `rewrite.ReplacePathInBytesWithJSONEscape` when the bytes pass through a JSON emitter that serialises `/` as `\/`. Surfaces deliberately not rewritten get a one-line reason: opaque by policy, runtime-only sidecar, filename keyed by something other than the path.

**Don't** `strings.ReplaceAll` user paths — it corrupts prefix-sharing names. Don't rewrite only top-level fields — embedded paths in nested JSON, JSONL bodies, and free text get left pointing at the old project. Don't use the non-escape variant on bytes that round-trip through `encoding/json` — `\/` survives the round trip and slips through the path-boundary check.

**See** `internal/rewrite/README.md`.

## Decide whether to inspect the bytes of a user data category

State the category's opacity policy first. File-history snapshot bodies are opaque. Archive entry bodies below the manifest contract are opaque. Transcripts and history lines have a documented JSONL contract. For non-opaque categories, the owning module's README §Contracts states what is read, what is refused, and what residual risk the caller carries — before any classifier appears in code.

**Don't** introduce a byte-classification heuristic — null-byte scan, magic-prefix detection, encoding sniff, "is this text". Every classifier carries residual misclassification risk: a binary format with no magic prefix gets treated as text and rewritten; text containing a null byte gets treated as binary and skipped. Opacity exists to avoid those paths entirely.

**See** `docs/architecture.md` §File-history policy and the owning module's README §Contracts.

## Add a category, session-keyed directory, or user-wide rewrite target

Extend the registry. New export category → one entry in the owning tool's `Categories()` list, validated through `manifest.ApplyToolCategories` and `manifest.BuildToolCategoryEntries`. New session-keyed directory under `~/.claude/` → one `claude.Registries` row (surfaced through `claude.SessionKeyedGroups`), its `Category` field naming one of the tool's registered categories. New user-wide file under `~/.claude/` whose contents reference user paths → one `claude.Registries` row surfaced through `claude.UserWideRewriteTargets`. The drift-guard test for the parity invariant ships in the same change.

**Don't** hard-code a category slice in `cmd/cc-port/`. Don't enumerate the directory in callers (move, export, importer, ui). Don't maintain a parallel list of user-wide rewrite files in move's logic. Don't defer the parity test — drift that lands in the original change is invisible to a test added later.

**See** `internal/manifest/README.md` and `internal/tool/claude/README.md` §Session-keyed registry.

## Write a command body that mutates user state

Wrap the entire body in `lock.WithLock` before any write. Run pre-flight checks first: encoded-directory collision (different paths can encode to the same dir — `/x/my project` and `/x/my-project` collide), project-identity cross-check (the encoded dir's session contents identify the project; trust the dir only after the cross-check), TTY presence (any `huh` form fails opaquely without a controlling terminal). Stage all writes; promote atomically; on partial failure, restore originals from a tracker that captured them before the writes. Sensitive files (`history.jsonl`, `.claude.json`, transcripts) write at `0o600`.

**Don't** write directly without `lock.WithLock` — concurrent invocations corrupt shared state. Don't trust that `claude.EncodePath(path)` lands at a unique on-disk dir — two distinct paths can collide and splice one project's data into another's. Don't run `huh` prompts without a TTY preflight. Don't rely on default file mode — `0o644` is wrong for files containing user prompts.

**See** `internal/lock/README.md` and `internal/ui/README.md` §Interactive prompts require a TTY.

## Stage a write across symlinked or cross-volume destinations

`fsutil.ResolveExistingAncestor` walks the destination parent through symlinks to a real ancestor; place the temp on the same real filesystem; `os.Rename` is then atomic.

**Don't** place the temp as a bare sibling of the destination without resolving the parent. A symlinked-parent destination on a different volume hits `EXDEV` mid-promote, leaving partial state behind.

**See** `internal/fsutil/README.md`.

## Handle arbitrarily-sized user-supplied input

Stream through `pipeline.WriterStage` / `pipeline.ReaderStage`. Reach for terminal `pipeline.MaterializeStage` only when random access is required (zip readers, etc.). Thread `context.Context` from the public entry point so Ctrl-C cancels cooperatively. Cap line length on `bufio.Scanner` reads — `claude.MaxHistoryLine` for `history.jsonl`, an explicit `bufio.Scanner.Buffer` for any other untrusted line-oriented input.

**Don't** buffer whole user-supplied input in memory before processing — memory grows without bound and Ctrl-C kills the process mid-write. Don't omit `context.Context` from a long-running entry point. Don't rely on the default `bufio.Scanner` buffer — its 64 KiB cap silently truncates lines that exceed it.

**See** `docs/architecture.md` §Pipeline composition and `internal/scan/README.md`.

## Manage closers across pipeline stages

Each stage returns its closer alongside its data; the runner walks every closer and joins their errors via `errors.Join`. The runner is idempotent — calling it twice does nothing the second time.

**Don't** chain manual `defer close(...)` calls per stage and hand-write a `closed bool` guard on each closer. Don't drop the second `Close` error when the first errors.

**See** `docs/architecture.md` §Pipeline composition.

## Propagate errors from unwind paths

Capture every error from `Close`, `restore()`, rollback, or stat-error paths and join into the caller's return via `errors.Join`. Stat errors that are not `errors.Is(err, fs.ErrNotExist)` get wrapped and returned.

**Don't** write `_ = thing.Close()`. Don't treat `restore()` as `void`. Don't treat "stat errors except `NotExist`" as "no conflict" — that quietly passes permission errors through as if the destination did not exist.

## Define a returnable error

Define returnable errors as sentinel values (`ErrEntryCapExceeded`) or typed errors (`UnknownArchiveEntryError{Entry: name}`), and let callers compare via `errors.Is` and `errors.As`. The error identity is the public contract; the message is the human-readable surface, free to be reworded later. List the sentinels and typed-error shapes in the owning module's README §Errors so callers know what to discriminate against.

**Don't** match on `err.Error()` substrings. Don't compose errors by string concatenation that the caller is expected to parse back. Brittle string-matching breaks the moment the message is reworded, and tests against literal substrings encode the wording as the contract instead of the identity.

**See** `internal/importer/README.md` §Errors.

## Plug an injectable dependency into a function

When a function needs a callable, output stream, time source, randomness source, or external service, the dependency enters via a function parameter, a `WithX` option on the surrounding `Run` or constructor call, or a constructor field on the type that owns the function. For genuinely process-wide values where parameter passing would leak through every layer (`os.Stdout`, default randomness), an unexported package-level seam (`var stdoutWriter io.Writer = os.Stdout`) reads the global once and lets a test reassign it under `t.Cleanup`.

**Don't** reach for the global directly inside the function (`os.Stdout`, free `os.Getenv`, `time.Now()` in production logic, hidden singletons). Don't add a public package-level function variable callers can mutate. Globals masquerading as seams hide injection points from the type system and grow as new dependencies arrive.

## Grow code adjacent to an extracted shape

Extract now. File split when one file carries multiple concerns. New module under `internal/<name>` when logic duplicates across cmd or across modules. New pipeline stage when an existing stage chain wants the work. `WithX` wrapper when a setup-teardown pair repeats at multiple call sites. Shared primitive in `internal/fsutil`, `internal/rewrite`, `internal/scan` when a stdlib pattern is being reimplemented inline.

**Don't** keep adding to one file because the diff is smaller. Don't reimplement a wrapped pattern inline because "extracting can come later". cc-port optimises every change for the cleanest end state, not the smallest diff (`CLAUDE.local.md`).

## Cross a module boundary

`cmd/` owns ordering and policy. When two `internal/` modules need to compose, the composition lives in `cmd/` or in a new `internal/` shared primitive. Each `internal/<name>` stays focused on its domain and exposes a stage-shaped API that cmd assembles.

**Don't** import a sibling `internal/` module to assemble a pipeline inside the current one. The result is duplicated assembly across modules and the original archive source opening multiple times.

**See** `docs/architecture.md`.

## Hold a host-system assumption about Claude Code

Pin the assumption to a fixture under `testdata/dotclaude/` or to an integration test that exercises real host behavior. Two host transformations are canonical and load-bearing: Claude Code resolves symlinks via the filesystem before encoding (`/tmp/foo` → `-private-tmp-foo` on macOS), so `claude.ResolveProjectPath` precedes `claude.EncodePath`; Claude Code's JSON emitters serialise `/` as `\/`, so path rewrites of JSON-emitter output use `rewrite.ReplacePathInBytesWithJSONEscape`. New assumptions extend the fixture set or carry an integration test in the same change.

**Don't** ground a host-system claim in recall ("Claude Code probably writes…"). Don't accumulate the assumption as an inline comment in production code — the moment Claude Code's behavior shifts, the unverified comment lies.

**See** `testdata/dotclaude/`.

## Reverse a guard, validation, or policy added deliberately

Name the prior reason in the change. State what new evidence rules it out — analysis showing the race cannot occur, a test that exercises the prior failure mode and now passes, a policy reversal with an explicit rationale. Search for callers and adjacent code that depend on the prior behavior.

**Don't** remove because "it's no longer needed" without that work. The guard was added against a real failure; removing it without naming the failure re-introduces it.
