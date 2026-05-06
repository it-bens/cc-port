# internal/credentials

## Purpose

Resolves AWS credentials for cc-port's s3 remote from a layered set of sources: a `.env`-style credentials file, `AWS_*` environment variables, and an interactive TTY prompt. The resolved set is returned as an `aws.CredentialsProvider` (static credentials) consumed by `internal/remote/`'s s3 opener.

## Public API

- `Resolve(ctx, opts ResolveOptions) (aws.CredentialsProvider, error)`: walks file then env then prompt with file-first conflict precedence; returns `(nil, nil)` when no source contributed any field (signals SDK default-chain fallback).
- `ResolveOptions{Path string, Prompt bool}`: configures Resolve. Empty `Path` disables the file source. `Prompt: false` makes incomplete-resolution a hard error.
- `IncompleteCredentialsError`: typed; carries `MissingFields []string` and `TriedSources []string`.
- `FileParseError`: typed; carries `Path string`, `Line int` (0 for whole-file failures), and a wrapped underlying error.

### Errors

- `ErrFilePermissionsTooPermissive`: returned by `Resolve` when the file at `ResolveOptions.Path` has mode bits beyond 0600. Tests assert via `errors.Is`.
- `ErrPromptUnavailable`: returned by `Resolve` when a missing required field would trigger a prompt but `/dev/tty` cannot be opened. Tests assert via `errors.Is`.
- `*IncompleteCredentialsError`: returned by `Resolve` when at least one source contributed and a required field still misses, but no further source can fill it (typically `Prompt: false`). `MissingFields` and `TriedSources` carry diagnostic context. Tests assert via `errors.As`.
- `*FileParseError`: returned by `parseFile` and surfaced through `Resolve` when `Path` is set. `Path`, `Line`, and `Unwrap()` carry context; `Line == 0` signals whole-file failure (read error, no recognized keys). Tests assert via `errors.As`.
- `fmt.Errorf("canceled: %w", ctx.Err())`: returned by `Resolve` when ctx fires during the prompt; the wrapped value is `context.Canceled`. Tests assert via `errors.Is(err, context.Canceled)`.

The error inventory follows the project's typed-errors discipline established by commit `5cedc0d`. No `err.Error()` substring matching anywhere.

## Contracts

### Source layering and precedence

Used by `cmd/cc-port` push and pull.

#### Handled

- File source (when `Path` set) parses `KEY=VALUE` lines, recognizes `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`. File mode must be at most 0600.
- Env source reads the same three variables from `os.Getenv`.
- Prompt source asks for each missing required field individually; never re-prompts for fields already supplied.
- Conflict precedence: file beats env; env fills file's gaps; prompt fills any required field still missing.
- SDK fallback: when no source contributed any field, `Resolve` returns `(nil, nil)`.

#### Refused

These paths abort with no provider returned and no further source attempted:

- File mode more permissive than 0600. The 0600 ceiling is the project's secret-handling convention (matches the SSH `~/.ssh/id_*` and AWS CLI `~/.aws/credentials` expectation). Surfaced as `ErrFilePermissionsTooPermissive` (see §Errors). Relaxing this requires a spec change.
- File contributes no recognized credential fields. Empty file, comment-only file, or a file whose only well-formed lines are all unknown keys all qualify. The path is explicitly opted into via `--credentials-file`, so a contribution of zero recognized fields signals misconfiguration, not a fall-through. Surfaced as `*FileParseError` with `Line == 0` and `Unwrap() == errEmptyFile`.
- Malformed line (not `KEY=VALUE` after comment / blank skip). The 1-based line number is recorded so the user can fix the offending row. Surfaced as `*FileParseError` with `Line == N` and `Unwrap() == errMalformedLine`.
- Line exceeding the per-line cap (`maxScannerLine`, 64 KiB). The scanner aborts with `bufio.ErrTooLong` rather than silently truncating. Surfaced as `*FileParseError` with `Line == 0` and `Unwrap() == bufio.ErrTooLong`.
- `Prompt: false` and required fields still missing after file and env. Returning `*IncompleteCredentialsError` with `MissingFields` and `TriedSources` lets the cmd layer print a precise diagnostic without copy-matching.
- Prompt requested but `/dev/tty` cannot be opened. Surfaced as `ErrPromptUnavailable`.
- Prompt canceled via `ctx`. Surfaced as `fmt.Errorf("canceled: %w", ctx.Err())`; tests assert via `errors.Is(err, context.Canceled)`.

#### Not covered

- Credential rotation: the resolved static provider holds a single snapshot. Long-running operations that outlive STS-issued credentials are not in scope.
- Credential helpers (`op read`, `pass show`, `aws-vault exec`): out of scope. Future spec may add a fourth source layer above the prompt; the resolver source-list shape is extension-friendly.
- System keychain integration: out of scope.

## Quirks

The cancellation seam closes the `/dev/tty` handle when `ctx.Done` fires, which causes the blocked `term.ReadPassword` call (from `github.com/charmbracelet/x/term`) to return; the helper then returns `fmt.Errorf("canceled: %w", ctx.Err())`. This preserves the cancellation contract introduced by commit `cdcb4e2` (Ctrl-C aborts within one read cycle). The trailing newline write (so the next CLI line does not run into the masked prompt) is `defer`-best-effort: a failed newline does not invalidate the secret already read.

This module owns echo-suppressed single-field secret prompts; `internal/ui` owns the form-prompt path. See [`docs/architecture.md`](../../docs/architecture.md) §TTY-prompt ownership split.

## Tests

`file_test.go` covers parser happy path, mode rejection, empty-file rejection, malformed-line rejection, and unknown-key tolerance using `t.TempDir()` and `os.WriteFile` with explicit mode bits in the third argument. No `testdata/` directory: contents are short enough to live next to the assertion.

`env_test.go` covers env reads via `t.Setenv`.

`resolver_test.go` covers every layering and precedence scenario, the incomplete-error path, the prompt-cancellation path, and the SDK-fallback path. Tests use a `fakePrompter` constructed inline because tests live in `package credentials` (the carve-out from the writing-tests skill: the prompt-injection seam cannot be observed externally without widening the public API).

The real `osTTYPrompter` is not unit-tested: CI has no `/dev/tty`. Interactive smoke testing is the only coverage.
