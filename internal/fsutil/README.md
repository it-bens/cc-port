# internal/fsutil

## Purpose

Small shared filesystem helpers used across cc-port: recursive directory copy, and a path-resolution helper that finds the longest existing ancestor of a path, evaluates symlinks on it, and re-attaches any missing tail.

## Public API

- `CopyDir(source, destination string) error` — recursively copy a directory tree, preserving file and directory permissions. Regular files are streamed via `io.Copy`. Symlinks are replicated as symlinks (their target strings are written verbatim and never followed for content). Irregular entries (sockets, FIFOs, devices) fail-hard. Writes go through an `os.Root` opened on `destination` so a malformed relative path cannot land outside.
- `ResolveExistingAncestor(absDir string) (string, error)` — walk `absDir` upward to the longest prefix that exists on disk, run `filepath.EvalSymlinks` on that prefix, and re-attach any missing trailing components unchanged. Requires an absolute path; see §Contracts.

## Contracts

### Absolute-path contract for `ResolveExistingAncestor`

`ResolveExistingAncestor` **requires an absolute path**. Passing a relative path is a programmer error at the caller's layer, not an operational error, so the helper **panics** rather than silently `filepath.Abs`-ifying or returning a checkable error.

The reasoning: most callers would unwrap a returned error and propagate it, hiding the origin; a silent `Abs`-ification would produce a surprising CWD-relative result that only surfaces under an unusual working directory in production. A panic surfaces the misuse at the exact call site during test or staging.

Handled — inputs that conform to the contract:

- An absolute path whose every component exists. Returns the `EvalSymlinks` form of the full path.
- An absolute path whose trailing components do not yet exist. Returns the `EvalSymlinks` form of the longest existing prefix, with the missing tail re-attached unchanged, so callers like `os.MkdirAll` create the tail on the resolved filesystem.
- `"/"`. Returns `"/"` unchanged.

Refused — returns an error without modifying anything:

- A path whose longest existing prefix points through a broken symlink. Wrapped as `resolve symlinks for %q: %w`.
- A stat error other than `os.ErrNotExist` during the walk-up (e.g. permission denied on an intermediate component). Wrapped as `stat %q: %w`.

Not covered — programmer-error inputs that panic:

- A relative path (including the empty string). Panics with `fsutil.ResolveExistingAncestor: path must be absolute, got %q` — do not soften this to a returned error. Callers that cannot guarantee an absolute path must call `filepath.Abs` themselves first.

### Symlink replication for `CopyDir`

`CopyDir` replicates symlinks as symlinks — `os.Readlink` on the source, `Root.Symlink` on the destination. It never calls `os.ReadFile` on a walked entry, so a source tree containing `link -> /etc/passwd` produces a destination symlink pointing at `/etc/passwd`, not a regular file containing its bytes.

Handled — inputs this contract covers:

- Regular files — streamed via `io.Copy` at the source file's mode.
- Directories — created via `Root.MkdirAll` at the source directory's mode, then re-chmodded in case a parent was created earlier at a coarser mode.
- Symlinks (file or directory) — replicated via `Root.Symlink` with the target string read from `os.Readlink`. `filepath.WalkDir` does not descend into symlinked directories, so loops and cross-volume escapes are avoided.

Refused — inputs that fail-hard:

- Irregular entries (sockets, FIFOs, devices, anything where `fs.DirEntry.Type()` is not `Dir`, `Regular`, or `Symlink`). Returns an error naming the path and mode.

Not covered — out-of-scope concerns:

- Cross-device boundaries — no special handling; if the destination lives on a different filesystem from the source, the copy works but a subsequent `os.Rename` onto the destination by a caller may fail with `EXDEV`. Callers that require rename atomicity own this concern.
- Ownership (`uid`/`gid`) — preserved only to the extent the kernel applies it; `CopyDir` does not call `Chown`.

## Quirks

- `Root.Chmod` on Unix is vulnerable to a symlink-swap race between file creation and mode application (`go doc os.Root`). cc-port is not affected in its current use because we own the destination and no adversarial process races the copy; the residual is documented so future contributors don't have to rediscover it.

## Tests

`CopyDir` has dedicated test files — `copy_test.go` covers source-directory mode preservation (including the bug case where a `0700` source was previously flattened to `0750`), nested mixed modes, and file mode preservation; `copy_symlink_test.go` covers symlink replication, symlink-to-directory replication, and large-file streaming; `copy_unix_test.go` (build-tagged `linux || darwin`) rejects FIFOs as irregular entries. `CopyDir` is also exercised transitively by `internal/move/move_test.go` and `internal/testutil/fixture_test.go`.

`ResolveExistingAncestor` has a dedicated test file `paths_test.go` covering: symlink resolution on an existing path, single and multiple missing trailing components preserved, `/` pass-through, broken symlink error, non-ENOENT stat error, and both panic cases (relative input, empty input).

## References

- `os.Root` — local authoritative: `go doc os.Root` · online supplement: https://pkg.go.dev/os#Root
- Conceptual framing: _Traversal-resistant file APIs in Go 1.24_ — https://go.dev/blog/osroot
