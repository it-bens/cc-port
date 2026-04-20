# internal/fsutil — agent notes

## Before editing

- `ResolveExistingAncestor` panics on a relative path; never soften to a returned error or a silent `filepath.Abs` call inside the helper (README §Absolute-path contract for ResolveExistingAncestor).
- `ResolveExistingAncestor` preserves non-existent trailing components so `os.MkdirAll` can create them on the resolved filesystem; do not `EvalSymlinks` the full input path as a one-liner (README §Absolute-path contract for ResolveExistingAncestor).
- `CopyDir` never follows symlinks for content; it replicates them via `os.Readlink` + `Root.Symlink`. Do not reintroduce `os.ReadFile` on a walked path (README §Symlink replication for CopyDir).

## Navigation

- Copy: `copy.go:CopyDir`.
- Path resolution: `paths.go:ResolveExistingAncestor`.
- Tests: `copy_test.go`, `copy_symlink_test.go`, `copy_unix_test.go`, `paths_test.go`.
