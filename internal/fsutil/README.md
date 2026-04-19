# internal/fsutil

## Purpose

Small shared filesystem helpers used across cc-port: recursive directory copy, and a path-resolution helper that finds the longest existing ancestor of a path, evaluates symlinks on it, and re-attaches any missing tail.

## Public API

- `CopyDir(source, destination string) error` — recursively copy a directory tree, preserving file and directory permissions. Returns an error if any entry cannot be copied.
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

## Tests

`CopyDir` has a dedicated test file `copy_test.go` covering source-directory mode preservation (including the bug case where a `0700` source was previously flattened to `0750`), nested mixed modes, and file mode preservation; it is also exercised transitively by `internal/move/move_test.go` and `internal/testutil/fixture_test.go`.

`ResolveExistingAncestor` has a dedicated test file `paths_test.go` covering: symlink resolution on an existing path, single and multiple missing trailing components preserved, `/` pass-through, broken symlink error, non-ENOENT stat error, and both panic cases (relative input, empty input).
