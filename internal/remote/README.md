# internal/remote

## Purpose

Wraps `gocloud.dev/blob` with a narrow consumer-defined surface. Exposes a `Remote` type with `Open`, `Create`, `Stat`, and `Close`, plus pipeline `Source` and `Sink` stage types. URL-scheme dispatch (file://, s3://) is delegated to gocloud.dev.

## Public API

- `New(ctx, rawURL) (*Remote, error)`: opens a bucket. Supported schemes: `file:///path` and `s3://bucket?region=<r>`. Backend-native authentication (AWS SDK chain) is the operator's environment responsibility.
- `(*Remote).Open(ctx, name) (*Reader, error)`: returns a Reader for the archive at name. `Reader.Size()` reports the content length the bucket advertised on open (no stat round trip). Returns `ErrNotFound` when absent.
- `(*Remote).Create(ctx, name) (io.WriteCloser, error)`: returns a writer; close commits the upload.
- `(*Remote).Stat(ctx, name) (Attributes, error)`: size + ModTime. Returns `ErrNotFound` when absent.
- `(*Remote).Close() error`: releases the bucket connection.
- `(*Remote).URL() string`: returns the URL the Remote was opened with.
- `Attributes`: struct with Size and ModTime.
- `Reader`: typed handle returned by `Remote.Open`. Implements `io.Reader` and `io.Closer`; `Size() int64` reports the bucket's content length.
- `ErrNotFound`: sentinel for missing keys (translated from `gcerrors.NotFound`).
- `Source{Remote, Key}`: pipeline.ReaderStage. Returns the gocloud bucket reader as a streaming `View{Reader, Size}` and reports `Size` from `Reader.Size()` without a stat round trip. Random-access materialization is the downstream `pipeline.MaterializeStage`'s responsibility.
- `Sink{Remote, Key}`: pipeline.WriterStage. Returns the bucket writer as both writer and closer; closing commits the upload.

## Contracts

### Backend dispatch and error normalization

Used by `internal/sync` and `cmd/cc-port` push and pull.

#### Handled

- URL parsing and bucket construction via `blob.OpenBucket`.
- `gcerrors.NotFound` translation to the package's `ErrNotFound` sentinel on Open and Stat.
- `Reader.Size()` returns the gocloud-reported content length without a separate stat call. `Source.Open` populates `View.Size` from it.

#### Refused

- URLs with unregistered schemes (`gs://`, `azblob://`, `ssh://`). Until a driver is blank-imported, those return a wrapped error from `blob.OpenBucket`.

#### Not covered

- Multipart and resumable uploads. The bucket writer commits whole-archive on Close. Failure means no archive on the remote.

## Quirks

`Sink` returns the gocloud bucket writer directly without wrapping. Closing that writer commits the upload; closing failure means the archive is not visible. Pipeline runner closes follow chain order so the upload commits after any upstream filter (encryption) flushes.

`Source` returns the gocloud bucket reader directly via the `*remote.Reader` wrapper. `View.Size` is populated from the bucket's open response, so callers do not stat separately. `View.ReaderAt` is nil because the gocloud reader is not random-access; consumers that need random access compose `pipeline.MaterializeStage` downstream. The runner's close cascade owns `rc.Close`.

## Tests

`remote_test.go` covers New, Open, Create, Stat, Close, ErrNotFound translation, and a file-backend round-trip via `t.TempDir()`. `stages_test.go` covers Source streaming output (Reader populated, ReaderAt nil, Size from gocloud), validation of nil Remote and empty Key, and Sink round-trip behavior. All tests use the `mem://` driver via blank import.

S3-specific behavior (consistency, multipart-upload error paths) is not exercised in unit tests. Integration tests gated by `CC_PORT_S3_INTEGRATION_URL` cover real bucket scenarios when an operator opts in.
