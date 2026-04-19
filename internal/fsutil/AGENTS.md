# internal/fsutil — agent notes

Small shared filesystem helpers: recursive copy, path resolution. See `README.md` for the full contract.

## Before editing

- `ResolveExistingAncestor` panics on a relative path on purpose — programmer-error fail-hard, not a missing feature; never soften to a returned error or a silent `filepath.Abs` call inside the helper (README §Absolute-path contract for `ResolveExistingAncestor`).
- `ResolveExistingAncestor` preserves non-existent trailing components so `os.MkdirAll` can create them on the resolved filesystem — do not `EvalSymlinks` the full input path as a one-liner (README §Absolute-path contract for `ResolveExistingAncestor`).

## Navigation

- Copy: `copy.go:CopyDir`.
- Path resolution: `paths.go:ResolveExistingAncestor`.
- Tests: `copy_test.go`, `paths_test.go`.

Read `README.md` before changing anything under `## Contracts`.
