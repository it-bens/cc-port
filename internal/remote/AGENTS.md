# internal/remote -- agent notes

## Before editing

- The Remote struct is concrete; consumers (sync, cmd) define their own minimal interfaces against this type if they need test stubs (README §Public API).
- Every Open and Stat path translates `gcerrors.NotFound` to ErrNotFound. Do not return wrapped gocloud errors for the not-found case (README §Backend dispatch).
- Source materializes to a 0600 tempfile; removal is in Source.Close. Do not return gocloud's reader directly (README §Quirks).
- Adding a backend means blank-importing the driver in remote.go and documenting the URL scheme; no other code change (README §Backend dispatch and error normalization).

## Navigation

- Entry: `remote.go` (`New`, `Remote`, `Attributes`, `ErrNotFound`).
- Stages: `stages.go` (`Source`, `Sink`, `drainToTempfile`).
- Tests: `remote_test.go`, `stages_test.go`.
