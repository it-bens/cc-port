# internal/remote

## Purpose

Wraps `gocloud.dev/blob` with a narrow consumer-defined surface. Exposes a `Remote` type with `Open`, `Create`, `Stat`, and `Close`, plus pipeline `Source` and `Sink` stage types. URL-scheme dispatch (file://, s3://) is owned by cc-port via `buildMux` in `mux.go`.

## Public API

- `New(ctx, rawURL, deps Deps) (*Remote, error)`: opens a bucket through cc-port's owned `*blob.URLMux`. Supported schemes: `file:///path` and `s3://bucket?...`. The s3 path consumes `deps.Credentials` when non-nil; otherwise falls back to the AWS SDK default chain.
- `NewWithMux(ctx, rawURL, mux *blob.URLMux) (*Remote, error)`: black-box test seam for opening a Remote against a caller-supplied mux. Production callers use `New`; tests in `package remote_test` (and any future scenario needing a non-production driver such as `memblob`) construct a local `*blob.URLMux` and pass it here. The seam is exported because `package remote_test` cannot reach unexported helpers.
- `Deps`: dependency struct passed to `New`. Fields: `Credentials aws.CredentialsProvider` (nil-permitted, signals SDK default-chain fallback).
- `(*Remote).Open(ctx, name) (*Reader, error)`: returns a Reader for the archive at name. `Reader.Size()` reports the content length the bucket advertised on open (no stat round trip). Returns `ErrNotFound` when absent.
- `(*Remote).Create(ctx, name) (io.WriteCloser, error)`: returns a writer; close commits the upload.
- `(*Remote).Stat(ctx, name) (Attributes, error)`: size + ModTime. Returns `ErrNotFound` when absent.
- `(*Remote).Close() error`: releases the bucket connection.
- `(*Remote).URL() string`: returns the URL the Remote was opened with.
- `Attributes`: struct with Size and ModTime.
- `Reader`: typed handle returned by `Remote.Open`. Implements `io.Reader` and `io.Closer`; `Size() int64` reports the bucket's content length.
- `ErrNotFound`: sentinel for missing keys (translated from `gcerrors.NotFound`).
- `URLDoc`: curated `--help` text describing the URL formats accepted by `New` (`file://`, AWS S3, S3-compatible providers). The `push` and `pull` commands concatenate this string into their Long help.
- `Source{Remote, Key}`: pipeline.ReaderStage. Returns the gocloud bucket reader as a streaming `View{Reader, Size}` and reports `Size` from `Reader.Size()` without a stat round trip. Random-access materialization is the downstream `pipeline.MaterializeStage`'s responsibility.
- `Sink{Remote, Key}`: pipeline.WriterStage. Returns the bucket writer as both writer and closer; closing commits the upload.

## Contracts

### Backend dispatch and error normalization

Used by `internal/sync` and `cmd/cc-port` push and pull.

#### Handled

- URL parsing and bucket construction via a cc-port-owned `*blob.URLMux` (`buildMux` in `mux.go`). Schemes registered in one place: `file://` via the stock `&fileblob.URLOpener{}`; `s3://` via the cc-port-owned `s3Opener`.
- The s3 opener strips s3blob-specific query parameters (`ssetype`, `kmskeyid`, `accelerate`, `use_path_style`, `s3ForcePathStyle`, `disable_https`), passes the residue to `gocloud.dev/aws.V2ConfigFromURLParams`, optionally overrides `Credentials` from `Deps`, builds an `*s3.Client` with the appropriate `s3.Options` modifiers, and finally calls `s3blob.OpenBucket`. Path components on the URL wrap the bucket via `blob.PrefixedBucket`.
- `gcerrors.NotFound` translation to the package's `ErrNotFound` sentinel on Open and Stat.
- `Reader.Size()` returns the gocloud-reported content length without a separate stat call. `Source.Open` populates `View.Size` from it.

#### Refused

- URLs with unregistered schemes (`gs://`, `azblob://`, `ssh://`). Those return a wrapped error from the mux.

#### Not covered

- Multipart and resumable uploads. The bucket writer commits whole-archive on Close. Failure means no archive on the remote.

## Quirks

The s3 opener replicates inside cc-port what gocloud's stock `s3blob.URLOpener.OpenBucketURL` does internally: split URL params between AWS-config knobs and s3blob/s3 knobs, then assemble. The replication exists because gocloud's URL opener is closed to credential injection; the URL surface accepts no access key, by design. The published seam for an alternate credentials provider is the non-URL form `s3blob.OpenBucket(ctx, *s3.Client, name, *Options)`, which cc-port now uses.

`Sink` returns the gocloud bucket writer directly without wrapping. Closing that writer commits the upload; closing failure means the archive is not visible. Pipeline runner closes follow chain order so the upload commits after any upstream filter (encryption) flushes.

`Source` returns the gocloud bucket reader directly via the `*remote.Reader` wrapper. `View.Size` is populated from the bucket's open response, so callers do not stat separately. `View.ReaderAt` is nil because the gocloud reader is not random-access; consumers that need random access compose `pipeline.MaterializeStage` downstream. The runner's close cascade owns `rc.Close`.

## Tests

`remote_test.go` covers New, Open, Create, Stat, Close, ErrNotFound translation, and a file-backend round-trip via `t.TempDir()`. `stages_test.go` covers Source streaming output (Reader populated, ReaderAt nil, Size from gocloud), validation of nil Remote and empty Key, and Sink round-trip behavior.

`opener_s3_test.go` covers URL-param stripping (`stripS3BlobParams`) and credential override semantics on the resolved `aws.Config` returned by `awsConfigFromURL`. The override-set test asserts `cfg.Credentials.Retrieve` returns the static values; the empty-Deps test asserts the helper preserves gocloud's default provider type.

`testing_helpers_test.go` (in `package remote_test`) exposes `newForTest`, which builds a local `*blob.URLMux` registering `mem://` and `file://` and opens the Remote through `NewWithMux`. Production never registers `mem`.

S3-specific behavior (consistency, multipart-upload error paths) is not exercised in unit tests. Integration tests gated by `CC_PORT_S3_INTEGRATION_URL` cover real bucket scenarios when an operator opts in.
