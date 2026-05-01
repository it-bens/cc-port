# internal/file

## Purpose

Pipeline source and sink stages for local filesystem I/O. Wraps `os.Open` and `os.OpenFile` with mode 0600 enforcement on the write side.

## Public API

- `Source{Path}`: ReaderStage. Opens Path for reading; returns the `*os.File` as the View's `ReaderAt` plus an `io.Closer` that closes the file.
- `Sink{Path}`: WriterStage. Creates Path with mode 0600, `O_CREATE|O_WRONLY|O_TRUNC`. Returns the `*os.File` as both writer and closer. Existing files are truncated.

## Contracts

### Mode 0600 on sink

Used by every cc-port command that writes an archive to disk.

#### Handled

- Sink creates files with `0600` regardless of whether the archive is encrypted. Plaintext archives carry sensitive content too.
- Existing files are truncated and rewritten with `0600`.

#### Refused

- None at this layer. Path validation is the cmd-layer's responsibility.

#### Not covered

- Permission preservation on overwrite. An existing file's prior mode is replaced with `0600`, never restored.

## Tests

`file_test.go` covers: Source open existing, Source error on missing, Sink create new with mode 0600, Sink overwrite existing. POSIX-only mode test skipped on Windows.
