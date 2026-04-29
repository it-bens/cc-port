# internal/remote

## Purpose

Wraps `gocloud.dev/blob` with a narrow consumer-defined surface. Exposes a `Remote` type with `Open`, `Create`, `Stat`, and `Close`, plus pipeline `Source` and `Sink` stage types. URL-scheme dispatch (file://, s3://) is delegated to gocloud.dev.

## Public API

- `New(ctx, rawURL) (*Remote, error)`: opens a bucket. Supported schemes: `file:///path` and `s3://bucket?region=<r>`. Backend-native authentication (AWS SDK chain) is the operator's environment responsibility.
- `(*Remote).Open(ctx, name) (io.ReadCloser, error)`: returns a reader for the archive at name. Returns `ErrNotFound` when absent.
- `(*Remote).Create(ctx, name) (io.WriteCloser, error)`: returns a writer; close commits the upload.
- `(*Remote).Stat(ctx, name) (Attributes, error)`: size + ModTime. Returns `ErrNotFound` when absent.
- `(*Remote).Close() error`: releases the bucket connection.
- `(*Remote).URL() string`: returns the URL the Remote was opened with.
- `Attributes`: struct with Size and ModTime.
- `ErrNotFound`: sentinel for missing keys (translated from `gcerrors.NotFound`).
- `Source{Remote, Key}`: pipeline.ReaderStage. Drains bytes into a 0600 tempfile.
- `Sink{Remote, Key}`: pipeline.WriterStage. Returns the bucket writer directly.

## Contracts

### Backend dispatch and error normalization

Used by `internal/sync` and `cmd/cc-port` push and pull.

#### Handled

- URL parsing and bucket construction via `blob.OpenBucket`.
- `gcerrors.NotFound` translation to the package's `ErrNotFound` sentinel on Open and Stat.
- Tempfile lifecycle for Source: `0600` mode, removed on `Source.Close`.

#### Refused

- URLs with unregistered schemes (`gs://`, `azblob://`, `ssh://`). Until a driver is blank-imported, those return a wrapped error from `blob.OpenBucket`.

#### Not covered

- SIGKILL between Source.Open and Source.Close leaves a tempfile in `os.TempDir()`. Eventually cleaned by OS tempdir policy. Same residual risk as `internal/encrypt`'s decrypt-tempfile lifecycle.
- Multipart and resumable uploads. The bucket writer commits whole-archive on Close. Failure means no archive on the remote.

## Quirks

`Sink` returns the gocloud bucket writer directly without wrapping. Closing that writer commits the upload; closing failure means the archive is not visible. Pipeline runner closes follow chain order so the upload commits after any upstream filter (encryption) flushes.

`Source` always materializes to a tempfile because gocloud's reader is `io.ReadCloser`, not `io.ReaderAt`. The pipeline's import-side cores need random access. The tempfile is cheap for personal-use archives; range-reader optimization is a follow-up.

## Tests

`remote_test.go` covers New, Open, Create, Stat, Close, ErrNotFound translation, and a file-backend round-trip via `t.TempDir()`. `stages_test.go` covers Source and Sink round-trip behavior, validation of nil Remote and empty Key, and tempfile cleanup. All tests use the `mem://` driver via blank import.

S3-specific behavior (consistency, multipart-upload error paths) is not exercised in unit tests. Integration tests gated by `CC_PORT_S3_INTEGRATION_URL` cover real bucket scenarios when an operator opts in.
