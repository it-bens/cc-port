# internal/fsutil

## Purpose

Shared filesystem helpers for recursive directory copy and path resolution. `ResolveExistingAncestor` walks a path upward to the longest existing prefix, evaluates symlinks on it, and re-attaches any missing tail.

## Public API

- `CopyDir(ctx context.Context, source, destination string) error`: recursively copies a directory tree, preserving file and directory permissions. Symlinks are replicated as symlinks. Irregular entries fail-hard. Writes go through an `os.Root` opened on `destination`. `ctx.Err()` is checked at the top of every `WalkDir` callback so a cancelled context aborts within one iteration.
- `ResolveExistingAncestor(absDir string) (string, error)`: walks `absDir` upward to the longest prefix that exists on disk. Runs `filepath.EvalSymlinks` on that prefix and re-attaches any missing trailing components unchanged. Requires an absolute path. See §Contracts.

## Contracts

### Absolute-path contract for ResolveExistingAncestor

`ResolveExistingAncestor` requires an absolute path.

Called by `internal/claude` (`claude/paths.go`) and `internal/importer` (`importer/importer.go`).

#### Handled

- An absolute path whose every component exists. Returns the `EvalSymlinks` form of the full path.
- An absolute path whose trailing components do not yet exist. Returns the `EvalSymlinks` form of the longest existing prefix with the missing tail re-attached. Callers like `os.MkdirAll` can then create the tail on the resolved filesystem.
- `"/"`. Returns `"/"` unchanged.

#### Refused

- A path whose longest existing prefix points through a broken symlink. Wrapped as `resolve symlinks for %q: %w`.
- A stat error other than `os.ErrNotExist` during the walk-up (e.g. permission denied on an intermediate component). Wrapped as `stat %q: %w`.

#### Not covered

A relative path (including the empty string) is a programmer error at the caller's layer. The helper panics with `fsutil.ResolveExistingAncestor: path must be absolute, got %q`. Do not soften this to a returned error. Callers that cannot guarantee an absolute path must call `filepath.Abs` themselves first.

### Symlink replication for CopyDir

`CopyDir` replicates symlinks as symlinks. It calls `os.Readlink` on the source and `Root.Symlink` on the destination. A source tree containing `link -> /etc/passwd` produces a destination symlink pointing at `/etc/passwd`. The linked file's bytes are never read.

Called by `internal/move` (`move/execute.go`) and `internal/testutil` (`testutil/fixture.go`).

#### Handled

- Regular files: streamed via `io.Copy` at the source file's mode.
- Directories: created via `Root.MkdirAll` at the source directory's mode, then re-chmodded in case a parent was created earlier at a coarser mode.
- Symlinks (file or directory): replicated via `Root.Symlink` with the target string read from `os.Readlink`. `filepath.WalkDir` does not descend into symlinked directories, so loops and cross-volume escapes are avoided.

#### Refused

Irregular entries (sockets, FIFOs, devices, anything where `fs.DirEntry.Type()` is not `Dir`, `Regular`, or `Symlink`). Returns an error naming the path and mode.

#### Not covered

- Cross-device boundaries: no special handling. If destination and source are on different filesystems, the copy works. A subsequent `os.Rename` onto the destination may fail with `EXDEV`. Callers that require rename atomicity own this concern.
- Ownership (`uid`/`gid`): preserved only to the extent the kernel applies it. `CopyDir` does not call `Chown`.

## Quirks

`Root.Chmod` on Unix is vulnerable to a symlink-swap race between file creation and mode application (`go doc os.Root`). cc-port is not affected in its current use because we own the destination and no adversarial process races the copy. The residual is documented so future contributors don't rediscover it.

## Tests

`copy_test.go` covers directory mode preservation, nested mixed modes, and file mode preservation. `copy_symlink_test.go` covers symlink replication, symlink-to-directory replication, and large-file streaming. `copy_unix_test.go` (build-tagged `linux || darwin`) rejects FIFOs. `paths_test.go` covers symlink resolution, missing-tail preservation, `/` pass-through, broken symlink error, non-`ENOENT` stat error, and the two panic cases.
