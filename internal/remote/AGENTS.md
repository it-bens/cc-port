# internal/remote -- agent notes

## Before editing

- The Remote struct is concrete; consumers (sync, cmd) define their own minimal interfaces against this type if they need test stubs (README §Public API).
- Every Open and Stat path translates `gcerrors.NotFound` to ErrNotFound. Do not return wrapped gocloud errors for the not-found case (README §Backend dispatch and error normalization).
- `Remote.Open` returns `*Reader` carrying the bucket-reported content length; never call Stat just to populate Size (README §Public API).
- Adding a backend means writing a `BucketURLOpener` in `opener_<scheme>.go`, registering it in `mux.go`, and extending `URLDoc` with the scheme's user-facing examples; no other code change (README §Public API, §Backend dispatch and error normalization).
- The s3 opener accepts an `aws.CredentialsProvider` via `Deps.Credentials`; nil means SDK default chain. The opener never reads cc-port-specific credential sources directly; that lives in `internal/credentials/` (README §Backend dispatch and error normalization).

## Navigation

- Entry: `remote.go` (`New`, `NewWithMux`, `Deps`, `Remote`, `Attributes`, `ErrNotFound`).
- Mux: `mux.go` (`buildMux`).
- S3 opener: `opener_s3.go` (`s3Opener`, `stripS3BlobParams`, `awsConfigFromURL`).
- Stages: `stages.go` (`Source`, `Sink`).
- Tests: `remote_test.go`, `stages_test.go`, `opener_s3_test.go`, `testing_helpers_test.go`.
