# internal/remote -- agent notes

## Before editing

- The Remote struct is concrete; consumers (sync, cmd) define their own minimal interfaces against this type if they need test stubs (README §Public API).
- Every Open and Stat path translates `gcerrors.NotFound` to ErrNotFound. Do not return wrapped gocloud errors for the not-found case (README §Backend dispatch).
- `Remote.Open` returns `*Reader` carrying the bucket-reported content length; never call Stat just to populate Size (README §Public API).
- Adding a backend means blank-importing the driver in remote.go and extending `URLDoc` with the scheme's user-facing examples; no other code change (README §Public API, §Backend dispatch and error normalization).

## Navigation

- Entry: `remote.go` (`New`, `Remote`, `Attributes`, `ErrNotFound`).
- Stages: `stages.go` (`Source`, `Sink`).
- Tests: `remote_test.go`, `stages_test.go`.
