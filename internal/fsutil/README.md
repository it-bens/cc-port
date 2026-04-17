# internal/fsutil

## Purpose

One shared filesystem helper: recursive directory copy. Used by `internal/move` during copy-verify-delete and by `internal/testutil.SetupFixture` when staging fixtures.

## Public API

- `CopyDir(source, destination string) error` — recursively copy a directory tree, preserving file permissions. Returns an error if any entry cannot be copied.

## Tests

No dedicated test file. `CopyDir` is exercised transitively by `internal/move/move_test.go` and `internal/testutil/fixture_test.go`.
