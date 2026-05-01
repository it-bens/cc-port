# internal/file — agent notes

## Before editing

- Keep Sink mode at 0600. Plaintext archives carry sensitive content (README §Mode 0600 on sink).
- Source returns `*os.File` directly as the View's `ReaderAt` and as the `io.Closer`; do not introduce buffering or copies (README §Public API).

## Navigation

- Entry: `file.go` (`Source`, `Sink`).
- Tests: `file_test.go`.
