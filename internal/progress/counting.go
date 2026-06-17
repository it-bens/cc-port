package progress

import "io"

// CountingWriter wraps w so each successful write advances handle by the bytes
// actually written, not the bytes offered. A partial write that also returns
// an error still advances by what moved.
func CountingWriter(w io.Writer, handle PhaseHandle) io.Writer {
	return &countingWriter{inner: w, handle: handle}
}

type countingWriter struct {
	inner  io.Writer
	handle PhaseHandle
}

func (writer *countingWriter) Write(p []byte) (int, error) {
	n, err := writer.inner.Write(p)
	if n > 0 {
		writer.handle.Advance(int64(n))
	}
	return n, err
}

// CountingReaderAt wraps r so each positional read advances handle by the bytes
// actually read.
func CountingReaderAt(r io.ReaderAt, handle PhaseHandle) io.ReaderAt {
	return &countingReaderAt{inner: r, handle: handle}
}

type countingReaderAt struct {
	inner  io.ReaderAt
	handle PhaseHandle
}

func (reader *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := reader.inner.ReadAt(p, off)
	if n > 0 {
		reader.handle.Advance(int64(n))
	}
	return n, err
}
